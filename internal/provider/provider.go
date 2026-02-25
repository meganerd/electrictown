// Package provider defines the core interface and types for LLM provider adapters.
// All provider adapters (OpenAI, Anthropic, Ollama, etc.) implement the Provider
// interface, enabling provider-agnostic model routing.
package provider

import (
	"context"
	"io"
)

// Provider is the core interface that all LLM provider adapters must implement.
// It provides a unified API for chat completions across any LLM provider.
type Provider interface {
	// Name returns the provider identifier (e.g., "openai", "anthropic", "ollama").
	Name() string

	// ChatCompletion sends a chat completion request and returns the full response.
	ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// StreamChatCompletion sends a chat completion request and returns a stream
	// of response chunks. The caller must call Close() on the returned stream
	// when done.
	StreamChatCompletion(ctx context.Context, req *ChatRequest) (ChatStream, error)

	// ListModels returns the models available from this provider.
	ListModels(ctx context.Context) ([]Model, error)
}

// Role represents a message role in the conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message in a chat conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`

	// ToolCallID is set when Role is "tool" to identify which tool call this
	// message responds to.
	ToolCallID string `json:"tool_call_id,omitempty"`

	// ToolCalls contains any tool calls the assistant wants to make.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a tool/function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // always "function" for now
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments for a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// Tool defines a tool the model can call.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a callable function.
type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"` // JSON Schema object
}

// ChatRequest represents a provider-agnostic chat completion request.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
	Stream      bool      `json:"stream,omitempty"`

	// ProviderOptions holds provider-specific options that don't fit the
	// unified schema. Adapters can read these for provider-specific features.
	ProviderOptions map[string]interface{} `json:"provider_options,omitempty"`
}

// ChatResponse represents a provider-agnostic chat completion response.
type ChatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Message Message  `json:"message"`
	Usage   Usage    `json:"usage"`
	Done    bool     `json:"done"`
	Error   *APIError `json:"error,omitempty"`
}

// Usage tracks token consumption for cost tracking.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// APIError represents a structured error from a provider.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Status  int    `json:"status"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// ChatStream provides an iterator-style interface for streaming responses.
type ChatStream interface {
	// Next returns the next chunk from the stream. Returns io.EOF when the
	// stream is complete.
	Next() (*ChatStreamChunk, error)

	// Close releases resources associated with the stream.
	Close() error
}

// ChatStreamChunk represents a single chunk in a streaming response.
type ChatStreamChunk struct {
	ID    string       `json:"id"`
	Model string       `json:"model"`
	Delta MessageDelta `json:"delta"`
	Usage *Usage       `json:"usage,omitempty"` // present in final chunk
	Done  bool         `json:"done"`
}

// MessageDelta represents the incremental content in a stream chunk.
type MessageDelta struct {
	Role      Role       `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Model represents an available model from a provider.
type Model struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

// ErrorCode classifies errors for fallback routing decisions.
type ErrorCode string

const (
	ErrRateLimit     ErrorCode = "rate_limit"
	ErrContextWindow ErrorCode = "context_window"
	ErrAuth          ErrorCode = "auth"
	ErrTimeout       ErrorCode = "timeout"
	ErrServerError   ErrorCode = "server_error"
	ErrUnknown       ErrorCode = "unknown"
)

// ClassifyError examines an error and returns its ErrorCode for routing decisions.
func ClassifyError(err error) ErrorCode {
	if err == nil {
		return ErrUnknown
	}
	if apiErr, ok := err.(*APIError); ok {
		switch {
		case apiErr.Status == 429:
			return ErrRateLimit
		case apiErr.Status == 401 || apiErr.Status == 403:
			return ErrAuth
		case apiErr.Status >= 500:
			return ErrServerError
		case apiErr.Code == "context_length_exceeded":
			return ErrContextWindow
		}
	}
	return ErrUnknown
}

// Ensure APIError implements the error interface.
var _ error = (*APIError)(nil)

// Ensure io.EOF is available for stream consumers.
var _ = io.EOF
