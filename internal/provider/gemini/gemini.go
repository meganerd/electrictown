// Package gemini implements the provider.Provider interface for the Google Gemini API.
// It uses native net/http for all HTTP communication -- no external SDK.
package gemini

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
	defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	providerName   = "gemini"
)

// GeminiProvider implements provider.Provider using the Google Gemini REST API.
type GeminiProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// Option configures a GeminiProvider.
type Option func(*GeminiProvider)

// WithBaseURL overrides the default Gemini API base URL.
func WithBaseURL(url string) Option {
	return func(p *GeminiProvider) {
		p.baseURL = strings.TrimRight(url, "/")
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(p *GeminiProvider) {
		p.client = client
	}
}

// New creates a GeminiProvider with the given API key and options.
func New(apiKey string, opts ...Option) *GeminiProvider {
	p := &GeminiProvider{
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
func (p *GeminiProvider) Name() string {
	return providerName
}

// --- Gemini API types (wire format) ---

type geminiRequest struct {
	Contents          []geminiContent          `json:"contents"`
	SystemInstruction *geminiSystemInstruction  `json:"system_instruction,omitempty"`
	Tools             []geminiToolDeclaration  `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
}

type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                 `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall    `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolDeclaration struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	Error         *geminiError         `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

type geminiModelsResponse struct {
	Models []geminiModel `json:"models"`
}

type geminiModel struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// --- Request/Response translation ---

// toGeminiContents converts provider messages to Gemini contents, extracting
// system messages into a separate system instruction.
func toGeminiContents(msgs []provider.Message) ([]geminiContent, *geminiSystemInstruction) {
	var contents []geminiContent
	var sysInstruction *geminiSystemInstruction

	for _, m := range msgs {
		switch m.Role {
		case provider.RoleSystem:
			sysInstruction = &geminiSystemInstruction{
				Parts: []geminiPart{{Text: m.Content}},
			}

		case provider.RoleUser:
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			})

		case provider.RoleAssistant:
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{
					Role:  "model",
					Parts: parts,
				})
			}

		case provider.RoleTool:
			var respData map[string]any
			_ = json.Unmarshal([]byte(m.Content), &respData)
			if respData == nil {
				respData = map[string]any{"result": m.Content}
			}
			contents = append(contents, geminiContent{
				Role: "function",
				Parts: []geminiPart{
					{
						FunctionResponse: &geminiFunctionResponse{
							Name:     m.Name,
							Response: respData,
						},
					},
				},
			})
		}
	}

	return contents, sysInstruction
}

// toGeminiTools converts provider tools to Gemini tool declarations.
func toGeminiTools(tools []provider.Tool) []geminiToolDeclaration {
	if len(tools) == 0 {
		return nil
	}

	var decls []geminiFunctionDeclaration
	for _, t := range tools {
		decls = append(decls, geminiFunctionDeclaration{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	return []geminiToolDeclaration{{FunctionDeclarations: decls}}
}

// fromGeminiResponse converts a Gemini response to the provider ChatResponse.
func fromGeminiResponse(resp *geminiResponse, model string) *provider.ChatResponse {
	if len(resp.Candidates) == 0 {
		return nil
	}

	candidate := resp.Candidates[0]
	msg := fromGeminiContent(candidate.Content)

	chatResp := &provider.ChatResponse{
		Model:   model,
		Message: msg,
		Usage:   fromGeminiUsage(resp.UsageMetadata),
		Done:    true,
	}

	return chatResp
}

// fromGeminiContent converts a Gemini content to a provider Message.
func fromGeminiContent(c geminiContent) provider.Message {
	msg := provider.Message{
		Role: provider.RoleAssistant,
	}

	for _, part := range c.Parts {
		if part.Text != "" {
			msg.Content += part.Text
		}
		if part.FunctionCall != nil {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			msg.ToolCalls = append(msg.ToolCalls, provider.ToolCall{
				ID:   fmt.Sprintf("call_%s", part.FunctionCall.Name),
				Type: "function",
				Function: provider.FunctionCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	return msg
}

func fromGeminiUsage(u *geminiUsageMetadata) provider.Usage {
	if u == nil {
		return provider.Usage{}
	}
	return provider.Usage{
		PromptTokens:     u.PromptTokenCount,
		CompletionTokens: u.CandidatesTokenCount,
		TotalTokens:      u.TotalTokenCount,
	}
}

// --- HTTP helpers ---

func (p *GeminiProvider) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	fullURL := p.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, err
	}

	// Gemini uses API key as a query parameter.
	q := req.URL.Query()
	q.Set("key", p.apiKey)
	req.URL.RawQuery = q.Encode()

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (p *GeminiProvider) doJSON(req *http.Request, dst any) error {
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("gemini: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return p.parseErrorResponse(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("gemini: failed to decode response: %w", err)
	}
	return nil
}

func (p *GeminiProvider) parseErrorResponse(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var errResp struct {
		Error *geminiError `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != nil {
		return &provider.APIError{
			Code:    errResp.Error.Status,
			Message: errResp.Error.Message,
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
func (p *GeminiProvider) ChatCompletion(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	contents, sysInstruction := toGeminiContents(req.Messages)

	gemReq := geminiRequest{
		Contents:          contents,
		SystemInstruction: sysInstruction,
		Tools:             toGeminiTools(req.Tools),
	}

	// Map generation config parameters.
	if req.Temperature != nil || req.TopP != nil || req.MaxTokens != nil || len(req.Stop) > 0 {
		gemReq.GenerationConfig = &geminiGenerationConfig{
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			MaxOutputTokens: req.MaxTokens,
			StopSequences:   req.Stop,
		}
	}

	body, err := json.Marshal(gemReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: failed to marshal request: %w", err)
	}

	path := fmt.Sprintf("/models/%s:generateContent", req.Model)
	httpReq, err := p.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	var gemResp geminiResponse
	if err := p.doJSON(httpReq, &gemResp); err != nil {
		return nil, err
	}

	if gemResp.Error != nil {
		return nil, &provider.APIError{
			Code:    gemResp.Error.Status,
			Message: gemResp.Error.Message,
			Status:  gemResp.Error.Code,
		}
	}

	if len(gemResp.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: response contained no candidates")
	}

	chatResp := fromGeminiResponse(&gemResp, req.Model)
	return chatResp, nil
}

// StreamChatCompletion sends a streaming chat completion request and returns a ChatStream.
func (p *GeminiProvider) StreamChatCompletion(ctx context.Context, req *provider.ChatRequest) (provider.ChatStream, error) {
	contents, sysInstruction := toGeminiContents(req.Messages)

	gemReq := geminiRequest{
		Contents:          contents,
		SystemInstruction: sysInstruction,
		Tools:             toGeminiTools(req.Tools),
	}

	if req.Temperature != nil || req.TopP != nil || req.MaxTokens != nil || len(req.Stop) > 0 {
		gemReq.GenerationConfig = &geminiGenerationConfig{
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			MaxOutputTokens: req.MaxTokens,
			StopSequences:   req.Stop,
		}
	}

	body, err := json.Marshal(gemReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: failed to marshal request: %w", err)
	}

	path := fmt.Sprintf("/models/%s:streamGenerateContent", req.Model)
	httpReq, err := p.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Add alt=sse query parameter for streaming.
	q := httpReq.URL.Query()
	q.Set("alt", "sse")
	httpReq.URL.RawQuery = q.Encode()

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: stream request failed: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, p.parseErrorResponse(resp)
	}

	return &sseStream{
		reader: bufio.NewReader(resp.Body),
		body:   resp.Body,
		model:  req.Model,
	}, nil
}

// ListModels retrieves available models from the Gemini API.
func (p *GeminiProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	httpReq, err := p.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}

	var modelsResp geminiModelsResponse
	if err := p.doJSON(httpReq, &modelsResp); err != nil {
		return nil, err
	}

	models := make([]provider.Model, len(modelsResp.Models))
	for i, m := range modelsResp.Models {
		// Strip "models/" prefix from the name for the ID.
		id := strings.TrimPrefix(m.Name, "models/")
		models[i] = provider.Model{
			ID:       id,
			Provider: providerName,
			Name:     m.DisplayName,
		}
	}
	return models, nil
}

// --- SSE stream implementation ---

type sseStream struct {
	reader *bufio.Reader
	body   io.ReadCloser
	model  string
}

func (s *sseStream) Next() (*provider.ChatStreamChunk, error) {
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("gemini: stream read error: %w", err)
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

		// Gemini streaming does not use [DONE] sentinel like OpenAI,
		// but handle it defensively.
		if data == "[DONE]" {
			return nil, io.EOF
		}

		var gemResp geminiResponse
		if err := json.Unmarshal([]byte(data), &gemResp); err != nil {
			return nil, fmt.Errorf("gemini: failed to parse stream chunk: %w", err)
		}

		chunk := &provider.ChatStreamChunk{
			Model: s.model,
		}

		if gemResp.UsageMetadata != nil {
			usage := fromGeminiUsage(gemResp.UsageMetadata)
			chunk.Usage = &usage
		}

		if len(gemResp.Candidates) > 0 {
			candidate := gemResp.Candidates[0]

			// Extract text delta from parts.
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					chunk.Delta.Content += part.Text
				}
				if part.FunctionCall != nil {
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					chunk.Delta.ToolCalls = append(chunk.Delta.ToolCalls, provider.ToolCall{
						ID:   fmt.Sprintf("call_%s", part.FunctionCall.Name),
						Type: "function",
						Function: provider.FunctionCall{
							Name:      part.FunctionCall.Name,
							Arguments: string(argsJSON),
						},
					})
				}
			}

			// Map role.
			if candidate.Content.Role == "model" {
				chunk.Delta.Role = provider.RoleAssistant
			}

			if candidate.FinishReason == "STOP" || candidate.FinishReason == "MAX_TOKENS" {
				chunk.Done = true
			}
		}

		return chunk, nil
	}
}

func (s *sseStream) Close() error {
	return s.body.Close()
}

// Compile-time interface compliance checks.
var _ provider.Provider = (*GeminiProvider)(nil)
var _ provider.ChatStream = (*sseStream)(nil)
