// Package ollama implements the provider.Provider interface for Ollama,
// supporting both local instances and Ollama cloud via native net/http.
package ollama

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

// OllamaProvider implements provider.Provider for Ollama's REST API.
type OllamaProvider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a new OllamaProvider. The baseURL should be the Ollama server
// address (e.g., "http://localhost:11434" for local, "https://api.ollama.com"
// for cloud). The apiKey is optional and used for Bearer auth with cloud.
func New(baseURL string, apiKey string) *OllamaProvider {
	return &OllamaProvider{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{},
	}
}

// Name returns "ollama".
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// ChatCompletion sends a non-streaming chat request to Ollama and returns
// the full response.
func (p *OllamaProvider) ChatCompletion(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	ollamaReq := p.buildChatRequest(req, false)

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	p.setHeaders(httpReq)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: send request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, p.parseError(httpResp)
	}

	var ollamaResp ollamaChatResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}

	return p.convertResponse(&ollamaResp), nil
}

// StreamChatCompletion sends a streaming chat request to Ollama and returns
// a ChatStream. Ollama uses newline-delimited JSON (NDJSON), not SSE.
func (p *OllamaProvider) StreamChatCompletion(ctx context.Context, req *provider.ChatRequest) (provider.ChatStream, error) {
	ollamaReq := p.buildChatRequest(req, true)

	body, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	p.setHeaders(httpReq)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: send request: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		return nil, p.parseError(httpResp)
	}

	return &ollamaStream{
		scanner: bufio.NewScanner(httpResp.Body),
		body:    httpResp.Body,
		done:    false,
	}, nil
}

// ListModels queries the Ollama API for available models.
func (p *OllamaProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	p.setHeaders(httpReq)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: send request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, p.parseError(httpResp)
	}

	var tagsResp ollamaTagsResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&tagsResp); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}

	models := make([]provider.Model, len(tagsResp.Models))
	for i, m := range tagsResp.Models {
		models[i] = provider.Model{
			ID:       m.Name,
			Provider: "ollama",
			Name:     m.Name,
		}
	}
	return models, nil
}

// --- Internal helpers ---

func (p *OllamaProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

func (p *OllamaProvider) buildChatRequest(req *provider.ChatRequest, stream bool) ollamaChatRequest {
	messages := make([]ollamaMessage, len(req.Messages))
	for i, m := range req.Messages {
		msg := ollamaMessage{
			Role:    string(m.Role),
			Content: m.Content,
		}
		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]ollamaToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				var args map[string]interface{}
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				msg.ToolCalls[j] = ollamaToolCall{
					Function: ollamaFunctionCall{
						Name:      tc.Function.Name,
						Arguments: args,
					},
				}
			}
		}
	messages[i] = msg
	}

	ollamaReq := ollamaChatRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   stream,
	}

	// Map optional parameters to Ollama's options object.
	options := make(map[string]interface{})
	if req.Temperature != nil {
		options["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		options["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		options["num_predict"] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		options["stop"] = req.Stop
	}
	if len(options) > 0 {
		ollamaReq.Options = options
	}

	// Map tools to Ollama's format.
	if len(req.Tools) > 0 {
		ollamaReq.Tools = make([]ollamaTool, len(req.Tools))
		for i, t := range req.Tools {
			ollamaReq.Tools[i] = ollamaTool{
				Type: t.Type,
				Function: ollamaToolFunction{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			}
		}
	}

	return ollamaReq
}

func (p *OllamaProvider) convertResponse(resp *ollamaChatResponse) *provider.ChatResponse {
	msg := provider.Message{
		Role:    provider.Role(resp.Message.Role),
		Content: resp.Message.Content,
	}

	if len(resp.Message.ToolCalls) > 0 {
		msg.ToolCalls = make([]provider.ToolCall, len(resp.Message.ToolCalls))
		for i, tc := range resp.Message.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Function.Arguments)
			msg.ToolCalls[i] = provider.ToolCall{
				ID:   fmt.Sprintf("call_%d", i),
				Type: "function",
				Function: provider.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: string(argsJSON),
				},
			}
		}
	}

	return &provider.ChatResponse{
		ID:    fmt.Sprintf("ollama-%s-%d", resp.Model, resp.CreatedAt),
		Model: resp.Model,
		Message: msg,
		Usage: provider.Usage{
			PromptTokens:     resp.PromptEvalCount,
			CompletionTokens: resp.EvalCount,
			TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
		},
		Done: resp.Done,
	}
}

func (p *OllamaProvider) parseError(resp *http.Response) error {
	var errResp struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&errResp)

	msg := errResp.Error
	if msg == "" {
		msg = fmt.Sprintf("ollama: HTTP %d", resp.StatusCode)
	}

	return &provider.APIError{
		Code:    fmt.Sprintf("ollama_%d", resp.StatusCode),
		Message: msg,
		Type:    "ollama_error",
		Status:  resp.StatusCode,
	}
}

// --- Ollama API types ---

type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaMessage        `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
	Tools    []ollamaTool           `json:"tools,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaFunctionCall `json:"function"`
}

type ollamaFunctionCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ollamaTool struct {
	Type     string             `json:"type"`
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type ollamaChatResponse struct {
	Model           string        `json:"model"`
	CreatedAt       interface{}   `json:"created_at,omitempty"`
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	TotalDuration   int64         `json:"total_duration,omitempty"`
	PromptEvalCount int           `json:"prompt_eval_count,omitempty"`
	EvalCount       int           `json:"eval_count,omitempty"`
}

type ollamaTagsResponse struct {
	Models []ollamaModelInfo `json:"models"`
}

type ollamaModelInfo struct {
	Name  string `json:"name"`
	Model string `json:"model"`
	Size  int64  `json:"size"`
}

// --- Stream implementation ---

type ollamaStream struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
	done    bool
}

func (s *ollamaStream) Next() (*provider.ChatStreamChunk, error) {
	if s.done {
		return nil, io.EOF
	}

	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return nil, err
		}
		s.done = true
		return nil, io.EOF
	}

	line := s.scanner.Bytes()
	if len(line) == 0 {
		// Skip empty lines; try the next one.
		return s.Next()
	}

	var resp ollamaChatResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("ollama: decode stream chunk: %w", err)
	}

	chunk := &provider.ChatStreamChunk{
		ID:    fmt.Sprintf("ollama-%s", resp.Model),
		Model: resp.Model,
		Delta: provider.MessageDelta{
			Role:    provider.Role(resp.Message.Role),
			Content: resp.Message.Content,
		},
		Done: resp.Done,
	}

	if len(resp.Message.ToolCalls) > 0 {
		chunk.Delta.ToolCalls = make([]provider.ToolCall, len(resp.Message.ToolCalls))
		for i, tc := range resp.Message.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Function.Arguments)
			chunk.Delta.ToolCalls[i] = provider.ToolCall{
				ID:   fmt.Sprintf("call_%d", i),
				Type: "function",
				Function: provider.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: string(argsJSON),
				},
			}
		}
	}

	if resp.Done {
		s.done = true
		chunk.Usage = &provider.Usage{
			PromptTokens:     resp.PromptEvalCount,
			CompletionTokens: resp.EvalCount,
			TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
		}
	}

	return chunk, nil
}

func (s *ollamaStream) Close() error {
	s.done = true
	return s.body.Close()
}

// Compile-time interface check.
var _ provider.Provider = (*OllamaProvider)(nil)
