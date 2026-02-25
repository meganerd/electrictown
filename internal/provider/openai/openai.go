// Package openai implements the provider.Provider interface for the OpenAI API.
// It uses native net/http for all HTTP communication -- no external SDK.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/meganerd/electrictown/internal/provider"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	providerName   = "openai"
)

// OpenAIProvider implements provider.Provider using the OpenAI REST API.
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	orgID   string
	client  *http.Client
}

// Option configures an OpenAIProvider.
type Option func(*OpenAIProvider)

// WithBaseURL overrides the default OpenAI API base URL.
func WithBaseURL(url string) Option {
	return func(p *OpenAIProvider) {
		p.baseURL = strings.TrimRight(url, "/")
	}
}

// WithOrganization sets the OpenAI-Organization header on all requests.
func WithOrganization(orgID string) Option {
	return func(p *OpenAIProvider) {
		p.orgID = orgID
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(p *OpenAIProvider) {
		p.client = client
	}
}

// New creates an OpenAIProvider with the given API key and options.
func New(apiKey string, opts ...Option) *OpenAIProvider {
	p := &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		client:  http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the provider identifier.
func (p *OpenAIProvider) Name() string {
	return providerName
}

// --- OpenAI API types (wire format) ---

type oaiRequest struct {
	Model       string        `json:"model"`
	Messages    []oaiMessage  `json:"messages"`
	Tools       []provider.Tool `json:"tools,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	StreamOptions *oaiStreamOptions `json:"stream_options,omitempty"`
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiMessage struct {
	Role       provider.Role       `json:"role"`
	Content    string              `json:"content"`
	Name       string              `json:"name,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	ToolCalls  []provider.ToolCall `json:"tool_calls,omitempty"`
}

type oaiResponse struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Choices []oaiChoice `json:"choices"`
	Usage   *oaiUsage   `json:"usage,omitempty"`
	Error   *oaiError   `json:"error,omitempty"`
}

type oaiChoice struct {
	Index        int        `json:"index"`
	Message      oaiMessage `json:"message"`
	Delta        oaiMessage `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type oaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"` // can be string or null
}

type oaiModelsResponse struct {
	Data []oaiModel `json:"data"`
}

type oaiModel struct {
	ID string `json:"id"`
}

// --- Request/Response translation ---

func toOAIMessages(msgs []provider.Message) []oaiMessage {
	out := make([]oaiMessage, len(msgs))
	for i, m := range msgs {
		out[i] = oaiMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
			ToolCalls:  m.ToolCalls,
		}
	}
	return out
}

func fromOAIMessage(m oaiMessage) provider.Message {
	return provider.Message{
		Role:       m.Role,
		Content:    m.Content,
		Name:       m.Name,
		ToolCallID: m.ToolCallID,
		ToolCalls:  m.ToolCalls,
	}
}

func fromOAIUsage(u *oaiUsage) provider.Usage {
	if u == nil {
		return provider.Usage{}
	}
	return provider.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}

func oaiErrorCode(code any) string {
	if code == nil {
		return ""
	}
	if s, ok := code.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", code)
}

// --- HTTP helpers ---

func (p *OpenAIProvider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if p.orgID != "" {
		req.Header.Set("OpenAI-Organization", p.orgID)
	}
	return req, nil
}

func (p *OpenAIProvider) doJSON(req *http.Request, dst any) error {
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return p.parseErrorResponse(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("openai: failed to decode response: %w", err)
	}
	return nil
}

func (p *OpenAIProvider) parseErrorResponse(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var errResp struct {
		Error *oaiError `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != nil {
		return &provider.APIError{
			Code:    oaiErrorCode(errResp.Error.Code),
			Message: errResp.Error.Message,
			Type:    errResp.Error.Type,
			Status:  resp.StatusCode,
		}
	}

	return &provider.APIError{
		Code:    http.StatusText(resp.StatusCode),
		Message: string(body),
		Status:  resp.StatusCode,
	}
}

// --- Provider interface implementation ---

// ChatCompletion sends a non-streaming chat completion request.
func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	oaiReq := oaiRequest{
		Model:       req.Model,
		Messages:    toOAIMessages(req.Messages),
		Tools:       req.Tools,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Stop:        req.Stop,
		Stream:      false,
	}

	body, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to marshal request: %w", err)
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var oaiResp oaiResponse
	if err := p.doJSON(httpReq, &oaiResp); err != nil {
		return nil, err
	}

	if oaiResp.Error != nil {
		return nil, &provider.APIError{
			Code:    oaiErrorCode(oaiResp.Error.Code),
			Message: oaiResp.Error.Message,
			Type:    oaiResp.Error.Type,
		}
	}

	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("openai: response contained no choices")
	}

	choice := oaiResp.Choices[0]
	return &provider.ChatResponse{
		ID:      oaiResp.ID,
		Model:   oaiResp.Model,
		Message: fromOAIMessage(choice.Message),
		Usage:   fromOAIUsage(oaiResp.Usage),
		Done:    true,
	}, nil
}

// StreamChatCompletion sends a streaming chat completion request and returns a ChatStream.
func (p *OpenAIProvider) StreamChatCompletion(ctx context.Context, req *provider.ChatRequest) (provider.ChatStream, error) {
	oaiReq := oaiRequest{
		Model:       req.Model,
		Messages:    toOAIMessages(req.Messages),
		Tools:       req.Tools,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Stop:        req.Stop,
		Stream:      true,
		StreamOptions: &oaiStreamOptions{IncludeUsage: true},
	}

	body, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to marshal request: %w", err)
	}

	httpReq, err := p.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: stream request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, p.parseErrorResponse(resp)
	}

	return &sseStream{
		reader: bufio.NewReader(resp.Body),
		body:   resp.Body,
	}, nil
}

// ListModels retrieves available models from the OpenAI API.
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	httpReq, err := p.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}

	var modelsResp oaiModelsResponse
	if err := p.doJSON(httpReq, &modelsResp); err != nil {
		return nil, err
	}

	models := make([]provider.Model, len(modelsResp.Data))
	for i, m := range modelsResp.Data {
		models[i] = provider.Model{
			ID:       m.ID,
			Provider: providerName,
			Name:     m.ID,
		}
	}
	return models, nil
}

// --- SSE stream implementation ---

type sseStream struct {
	reader *bufio.Reader
	body   io.ReadCloser
}

func (s *sseStream) Next() (*provider.ChatStreamChunk, error) {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("openai: stream read error: %w", err)
		}

		line = strings.TrimSpace(line)

		// Skip empty lines (SSE frame boundaries).
		if line == "" {
			continue
		}

		// Skip SSE comments.
		if strings.HasPrefix(line, ":") {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			return nil, io.EOF
		}

		var oaiResp oaiResponse
		if err := json.Unmarshal([]byte(data), &oaiResp); err != nil {
			return nil, fmt.Errorf("openai: failed to parse stream chunk: %w", err)
		}

		chunk := &provider.ChatStreamChunk{
			ID:    oaiResp.ID,
			Model: oaiResp.Model,
		}

		if oaiResp.Usage != nil {
			usage := fromOAIUsage(oaiResp.Usage)
			chunk.Usage = &usage
		}

		if len(oaiResp.Choices) > 0 {
			delta := oaiResp.Choices[0].Delta
			chunk.Delta = provider.MessageDelta{
				Role:      delta.Role,
				Content:   delta.Content,
				ToolCalls: delta.ToolCalls,
			}
			if oaiResp.Choices[0].FinishReason != nil {
				chunk.Done = true
			}
		}

		return chunk, nil
	}
}

func (s *sseStream) Close() error {
	return s.body.Close()
}

// Compile-time interface compliance check.
var _ provider.Provider = (*OpenAIProvider)(nil)
var _ provider.ChatStream = (*sseStream)(nil)
