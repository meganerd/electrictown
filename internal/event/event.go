// Package event provides a typed event bus for decoupling orchestration from
// presentation. The bus fans out events to multiple subscribers (CLI output,
// TUI, future web UI) without blocking the orchestration pipeline.
package event

import (
	"sync"
	"time"
)

// Type identifies a specific orchestration event.
type Type string

const (
	// Run lifecycle.
	TaskStarted  Type = "task_started"
	RunCompleted Type = "run_completed"

	// Phase transitions.
	PhaseStarted   Type = "phase_started"
	PhaseCompleted Type = "phase_completed"

	// Decomposition.
	SubtaskDecomposed Type = "subtask_decomposed"

	// Worker execution.
	WorkerStarted   Type = "worker_started"
	WorkerProgress  Type = "worker_progress"
	WorkerCompleted Type = "worker_completed"
	WorkerFailed    Type = "worker_failed"

	// DAG execution.
	DAGWaveStarted   Type = "dag_wave_started"
	DAGWaveCompleted Type = "dag_wave_completed"

	// Guardrail.
	GuardrailRetry Type = "guardrail_retry"

	// Specialist routing.
	SpecialistAssigned Type = "specialist_assigned"

	// Cost tracking.
	CostUpdate Type = "cost_update"

	// RAG.
	RAGIngest Type = "rag_ingest"
	RAGQuery  Type = "rag_query"

	// Jina.
	JinaFetch Type = "jina_fetch"

	// Validation.
	ValidationResult Type = "validation_result"
)

// Event is a single typed occurrence in the orchestration pipeline.
type Event struct {
	Type      Type        `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
}

// New creates an event with the current timestamp.
func New(t Type, data interface{}) Event {
	return Event{Type: t, Timestamp: time.Now(), Data: data}
}

// --- Typed payloads ---

// TaskStartedData carries context for the start of a run.
type TaskStartedData struct {
	Task       string `json:"task"`
	ConfigPath string `json:"config_path"`
	LogDir     string `json:"log_dir"`
	Version    string `json:"version"`
}

// PhaseData carries context for phase transitions.
type PhaseData struct {
	Name     string        `json:"name"`
	Detail   string        `json:"detail,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
}

// SubtaskDecomposedData carries the decomposed subtask list.
type SubtaskDecomposedData struct {
	Subtasks []string `json:"subtasks"`
	HasDeps  bool     `json:"has_deps"`
}

// WorkerData carries context for worker events.
type WorkerData struct {
	Index    int           `json:"index"`
	Total    int           `json:"total"`
	Model    string        `json:"model,omitempty"`
	Subtask  string        `json:"subtask,omitempty"`
	Content  string        `json:"content,omitempty"`
	Tokens   int           `json:"tokens,omitempty"`
	TPS      float64       `json:"tps,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
	Error    string        `json:"error,omitempty"`
	Score    int           `json:"score,omitempty"`
	Flagged  bool          `json:"flagged,omitempty"`
}

// DAGWaveData carries context for DAG wave events.
type DAGWaveData struct {
	Wave     int   `json:"wave"`
	Indices  []int `json:"indices,omitempty"`
	Total    int   `json:"total"`
}

// GuardrailRetryData carries context for guardrail retries.
type GuardrailRetryData struct {
	Index   int    `json:"index"`
	Attempt int    `json:"attempt"`
	MaxRetries int `json:"max_retries"`
	Score   int    `json:"score"`
	Note    string `json:"note,omitempty"`
}

// SpecialistAssignedData carries specialist routing info.
type SpecialistAssignedData struct {
	Index      int    `json:"index"`
	Specialist string `json:"specialist"`
	Model      string `json:"model"`
}

// CostUpdateData carries cumulative cost info.
type CostUpdateData struct {
	TotalTokens      int     `json:"total_tokens"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	EstimatedCost    float64 `json:"estimated_cost,omitempty"`
}

// RAGData carries RAG event info.
type RAGData struct {
	Collection string `json:"collection"`
	Count      int    `json:"count"`
	Detail     string `json:"detail,omitempty"`
}

// JinaFetchData carries Jina fetch info.
type JinaFetchData struct {
	URL    string `json:"url"`
	Length int    `json:"length"`
	Error  string `json:"error,omitempty"`
}

// ValidationData carries validation result info.
type ValidationData struct {
	Index  int      `json:"index"`
	Passed bool     `json:"passed"`
	Errors []string `json:"errors,omitempty"`
}

// RunCompletedData carries run completion info.
type RunCompletedData struct {
	Duration  time.Duration `json:"duration"`
	OutputDir string        `json:"output_dir,omitempty"`
	LogDir    string        `json:"log_dir"`
}

// --- Bus ---

// defaultBufSize is the per-subscriber channel buffer size.
// Large enough to absorb render delays without dropping important events.
const defaultBufSize = 256

// Subscriber is a receive-only event channel.
type Subscriber <-chan Event

// Bus fans out events to registered subscribers. Thread-safe.
type Bus struct {
	mu          sync.RWMutex
	subscribers []chan Event
	closed      bool
}

// NewBus creates a new event bus.
func NewBus() *Bus {
	return &Bus{}
}

// Subscribe registers a new subscriber and returns the receive channel.
func (b *Bus) Subscribe() Subscriber {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Event, defaultBufSize)
	b.subscribers = append(b.subscribers, ch)
	return ch
}

// Publish sends an event to all subscribers. If a subscriber's buffer is full,
// the oldest event is dropped (non-blocking). Safe for concurrent use.
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for _, ch := range b.subscribers {
		select {
		case ch <- e:
		default:
			// Buffer full — drop oldest, then send.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- e:
			default:
			}
		}
	}
}

// Emit is a convenience: creates a new Event and publishes it.
func (b *Bus) Emit(t Type, data interface{}) {
	b.Publish(New(t, data))
}

// Close drains and closes all subscriber channels.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, ch := range b.subscribers {
		close(ch)
	}
}
