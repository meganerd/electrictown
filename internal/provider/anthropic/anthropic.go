// Package anthropic implements the provider.Provider interface for Anthropic's
// Messages API. It handles the significant structural differences between
// Anthropic's API and the provider-agnostic interface, including system message
// extraction, content block arrays, and SSE streaming.
package anthropic

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
	defaultBaseURL   = "https://api.anthropic.com"
	defaultMaxTokens = 4096
	apiVersion       = "2023-06-01"
	providerName     = "anthropic"
)

// AnthropicProvider implements provider.Provider for Anthropic's Messages API.
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// Option configures the AnthropicProvider.
type Option func(*AnthropicProvider)

// WithBaseURL overrides the default Anthropic API base URL.
// Useful for testing or proxy configurations.
func WithBaseURL(url string) Option {
	return func(p *AnthropicProvider) {
		p.baseURL = url
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(p *AnthropicProvider) {
		p.client = client
	}
}

// New creates a new AnthropicProvider with the given API key and options.
func New(apiKey string, opts ...Option) *AnthropicProvider {
	p := &AnthropicProvider{
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
func (p *AnthropicProvider) Name() string {
	return providerName
}

// --- Anthropic API request/response types ---

// anthropicRequest is the request body for POST /v1/messages.
type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	Stop        []string           `json:"stop_sequences,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
}

// anthropicMessage represents a message in Anthropic's format.
type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []anthropicContentBlock
}

// anthropicContentBlock represents a content block in a message.
type anthropicContentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`         // for tool_use blocks
	Name      string      `json:"name,omitempty"`       // for tool_use blocks
	Input     interface{} `json:"input,omitempty"`      // for tool_use blocks
	ToolUseID string      `json:"tool_use_id,omitempty"` // for tool_result blocks
	Content   string      `json:"content,omitempty"`     // for tool_result blocks (when used as nested)
}

// anthropicTool represents a tool definition in Anthropic's format.
type anthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}

// anthropicResponse is the response body from POST /v1/messages.
type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	Model      string                  `json:"model"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
	Error      *anthropicError         `json:"error,omitempty"`
}

// anthropicUsage tracks token counts.
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicError represents an API error from Anthropic.
type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// --- SSE streaming types ---

type sseMessageStart struct {
	Type    string            `json:"type"`
	Message anthropicResponse `json:"message"`
}

type sseContentBlockStart struct {
	Type         string                `json:"type"`
	Index        int                   `json:"index"`
	ContentBlock anthropicContentBlock `json:"content_block"`
}

type sseContentBlockDelta struct {
	Type  string   `json:"type"`
	Index int      `json:"index"`
	Delta sseDelta `json:"delta"`
}

type sseDelta struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`    // tool_use delta
	Name  string `json:"name,omitempty"`  // tool_use delta
	Input string `json:"input,omitempty"` // tool_use partial_json delta (comes as partial_json type)
}

type sseMessageDelta struct {
	Type  string         `json:"type"`
	Delta sseMessageMeta `json:"delta"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

type sseMessageMeta struct {
	StopReason string `json:"stop_reason"`
}

// --- Core methods ---

// ChatCompletion sends a non-streaming chat completion request.
func (p *AnthropicProvider) ChatCompletion(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	anthropicReq := p.buildRequest(req)
	anthropicReq.Stream = false

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, p.parseErrorResponse(respBody, resp.StatusCode)
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		return nil, fmt.Errorf("anthropic: unmarshal response: %w", err)
	}

	return p.convertResponse(&anthropicResp), nil
}

// StreamChatCompletion sends a streaming chat completion request and returns
// a ChatStream that yields chunks via SSE.
func (p *AnthropicProvider) StreamChatCompletion(ctx context.Context, req *provider.ChatRequest) (provider.ChatStream, error) {
	anthropicReq := p.buildRequest(req)
	anthropicReq.Stream = true

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("anthropic: read error response: %w", err)
		}
		return nil, p.parseErrorResponse(respBody, resp.StatusCode)
	}

	return &anthropicStream{
		reader: bufio.NewReader(resp.Body),
		body:   resp.Body,
	}, nil
}

// ListModels returns the known Anthropic models. Anthropic does not provide
// a list models endpoint, so we return a curated hardcoded list.
func (p *AnthropicProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	models := []struct {
		id   string
		name string
	}{
		{"claude-opus-4-20250918", "Claude Opus 4"},
		{"claude-sonnet-4-20250514", "Claude Sonnet 4"},
		{"claude-haiku-4-5-20251001", "Claude Haiku 4.5"},
		{"claude-3-5-sonnet-20241022", "Claude 3.5 Sonnet"},
		{"claude-3-5-haiku-20241022", "Claude 3.5 Haiku"},
		{"claude-3-opus-20240229", "Claude 3 Opus"},
	}

	result := make([]provider.Model, len(models))
	for i, m := range models {
		result[i] = provider.Model{
			ID:       m.id,
			Provider: providerName,
			Name:     m.name,
		}
	}
	return result, nil
}

// --- Request building ---

// buildRequest converts a provider.ChatRequest into an Anthropic API request,
// handling the critical differences: system message extraction, tool format
// translation, and max_tokens default.
func (p *AnthropicProvider) buildRequest(req *provider.ChatRequest) anthropicRequest {
	var systemPrompt string
	var messages []anthropicMessage

	for _, msg := range req.Messages {
		switch msg.Role {
		case provider.RoleSystem:
			// Anthropic uses a top-level system field, not a message.
			if systemPrompt != "" {
				systemPrompt += "\n\n"
			}
			systemPrompt += msg.Content

		case provider.RoleUser:
			messages = append(messages, p.convertUserMessage(msg))

		case provider.RoleAssistant:
			messages = append(messages, p.convertAssistantMessage(msg))

		case provider.RoleTool:
			// Anthropic expects tool results as user messages with tool_result blocks.
			messages = append(messages, p.convertToolResultMessage(msg))
		}
	}

	maxTokens := defaultMaxTokens
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}

	ar := anthropicRequest{
		Model:       req.Model,
		Messages:    messages,
		System:      systemPrompt,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
	}

	if len(req.Tools) > 0 {
		ar.Tools = make([]anthropicTool, len(req.Tools))
		for i, t := range req.Tools {
			ar.Tools[i] = anthropicTool{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: t.Function.Parameters,
			}
		}
	}

	return ar
}

func (p *AnthropicProvider) convertUserMessage(msg provider.Message) anthropicMessage {
	return anthropicMessage{
		Role:    "user",
		Content: msg.Content,
	}
}

func (p *AnthropicProvider) convertAssistantMessage(msg provider.Message) anthropicMessage {
	if len(msg.ToolCalls) == 0 {
		return anthropicMessage{
			Role:    "assistant",
			Content: msg.Content,
		}
	}

	// Assistant messages with tool calls need content block array.
	var blocks []anthropicContentBlock
	if msg.Content != "" {
		blocks = append(blocks, anthropicContentBlock{
			Type: "text",
			Text: msg.Content,
		})
	}
	for _, tc := range msg.ToolCalls {
		var input interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		blocks = append(blocks, anthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	return anthropicMessage{
		Role:    "assistant",
		Content: blocks,
	}
}

func (p *AnthropicProvider) convertToolResultMessage(msg provider.Message) anthropicMessage {
	// Anthropic expects tool results as user messages with tool_result content blocks.
	block := anthropicContentBlock{
		Type:      "tool_result",
		ToolUseID: msg.ToolCallID,
		Content:   msg.Content,
	}
	return anthropicMessage{
		Role:    "user",
		Content: []anthropicContentBlock{block},
	}
}

// --- Response conversion ---

// convertResponse translates an Anthropic response into the provider-agnostic format.
func (p *AnthropicProvider) convertResponse(resp *anthropicResponse) *provider.ChatResponse {
	msg := provider.Message{
		Role: provider.RoleAssistant,
	}

	var textParts []string
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			msg.ToolCalls = append(msg.ToolCalls, provider.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: provider.FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}
	msg.Content = strings.Join(textParts, "")

	totalTokens := resp.Usage.InputTokens + resp.Usage.OutputTokens

	return &provider.ChatResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Message: msg,
		Usage: provider.Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      totalTokens,
		},
		Done: true,
	}
}

// --- Error handling ---

func (p *AnthropicProvider) parseErrorResponse(body []byte, statusCode int) *provider.APIError {
	var errResp struct {
		Error anthropicError `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return &provider.APIError{
			Code:    errResp.Error.Type,
			Message: errResp.Error.Message,
			Type:    errResp.Error.Type,
			Status:  statusCode,
		}
	}
	return &provider.APIError{
		Code:    "unknown_error",
		Message: fmt.Sprintf("anthropic: unexpected status %d: %s", statusCode, string(body)),
		Type:    "unknown_error",
		Status:  statusCode,
	}
}

// --- HTTP helpers ---

func (p *AnthropicProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
}

// --- Streaming ---

// anthropicStream implements provider.ChatStream for Anthropic SSE responses.
type anthropicStream struct {
	reader *bufio.Reader
	body   io.ReadCloser
	id     string
	model  string
	done   bool
}

// Next reads and parses the next SSE event from the stream.
func (s *anthropicStream) Next() (*provider.ChatStreamChunk, error) {
	if s.done {
		return nil, io.EOF
	}

	for {
		event, data, err := s.readSSEEvent()
		if err != nil {
			return nil, err
		}

		switch event {
		case "message_start":
			var msg sseMessageStart
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				return nil, fmt.Errorf("anthropic: parse message_start: %w", err)
			}
			s.id = msg.Message.ID
			s.model = msg.Message.Model
			// message_start doesn't produce a user-visible chunk, continue.
			continue

		case "content_block_start":
			var block sseContentBlockStart
			if err := json.Unmarshal([]byte(data), &block); err != nil {
				return nil, fmt.Errorf("anthropic: parse content_block_start: %w", err)
			}
			// For tool_use blocks, emit the initial chunk with the tool call ID/name.
			if block.ContentBlock.Type == "tool_use" {
				return &provider.ChatStreamChunk{
					ID:    s.id,
					Model: s.model,
					Delta: provider.MessageDelta{
						ToolCalls: []provider.ToolCall{{
							ID:   block.ContentBlock.ID,
							Type: "function",
							Function: provider.FunctionCall{
								Name: block.ContentBlock.Name,
							},
						}},
					},
				}, nil
			}
			continue

		case "content_block_delta":
			var delta sseContentBlockDelta
			if err := json.Unmarshal([]byte(data), &delta); err != nil {
				return nil, fmt.Errorf("anthropic: parse content_block_delta: %w", err)
			}
			switch delta.Delta.Type {
			case "text_delta":
				return &provider.ChatStreamChunk{
					ID:    s.id,
					Model: s.model,
					Delta: provider.MessageDelta{
						Content: delta.Delta.Text,
					},
				}, nil
			case "input_json_delta":
				return &provider.ChatStreamChunk{
					ID:    s.id,
					Model: s.model,
					Delta: provider.MessageDelta{
						ToolCalls: []provider.ToolCall{{
							Function: provider.FunctionCall{
								Arguments: delta.Delta.Input,
							},
						}},
					},
				}, nil
			}
			continue

		case "content_block_stop":
			continue

		case "message_delta":
			var md sseMessageDelta
			if err := json.Unmarshal([]byte(data), &md); err != nil {
				return nil, fmt.Errorf("anthropic: parse message_delta: %w", err)
			}
			var usage *provider.Usage
			if md.Usage != nil {
				total := md.Usage.InputTokens + md.Usage.OutputTokens
				usage = &provider.Usage{
					PromptTokens:     md.Usage.InputTokens,
					CompletionTokens: md.Usage.OutputTokens,
					TotalTokens:      total,
				}
			}
			return &provider.ChatStreamChunk{
				ID:    s.id,
				Model: s.model,
				Delta: provider.MessageDelta{},
				Usage: usage,
			}, nil

		case "message_stop":
			s.done = true
			return &provider.ChatStreamChunk{
				ID:    s.id,
				Model: s.model,
				Delta: provider.MessageDelta{},
				Done:  true,
			}, nil

		case "ping":
			continue

		default:
			// Unknown event type, skip.
			continue
		}
	}
}

// Close releases the underlying HTTP response body.
func (s *anthropicStream) Close() error {
	if s.body != nil {
		return s.body.Close()
	}
	return nil
}

// readSSEEvent reads a single SSE event (event + data lines) from the stream.
func (s *anthropicStream) readSSEEvent() (event string, data string, err error) {
	var eventType string
	var dataLines []string

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && (eventType != "" || len(dataLines) > 0) {
				// Partial event at EOF — return what we have.
				return eventType, strings.Join(dataLines, "\n"), nil
			}
			return "", "", err
		}

		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// Empty line signals end of an event.
			if eventType != "" || len(dataLines) > 0 {
				return eventType, strings.Join(dataLines, "\n"), nil
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
		// Lines starting with ":" are comments, skip.
	}
}

// Compile-time verification that AnthropicProvider satisfies the Provider interface.
var _ provider.Provider = (*AnthropicProvider)(nil)
