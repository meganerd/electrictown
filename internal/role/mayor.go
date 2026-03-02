package role

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/meganerd/electrictown/internal/cost"
	"github.com/meganerd/electrictown/internal/provider"
)

// listItemPattern matches numbered lists (1. or 1)), bullet lists (- or * or bullet char).
var listItemPattern = regexp.MustCompile(`^\s*(?:\d+[.)]\s+|[-*]\s+|` + "\u2022" + `\s+)(.+)$`)

// boldPattern strips **text** and *text* markdown emphasis.
var boldPattern = regexp.MustCompile(`\*{1,2}([^*]+)\*{1,2}`)

// PlanResult holds the output of a Mayor.Plan call: a summary and discrete subtasks.
type PlanResult struct {
	Summary  string
	Subtasks []string
}

// WorkerResult represents the output from a single worker (polecat) execution.
type WorkerResult struct {
	Role        string
	Subtask     string
	Response    string
	Tokens      int
	Elapsed     time.Duration // time taken for the LLM call
	ReviewScore int           // 0 = not reviewed; 1-10 reviewer quality score
	ReviewNote  string        // brief reviewer feedback
	Flagged     bool          // true when ReviewScore < reviewer threshold
}

// MayorOption configures a Mayor during construction.
type MayorOption func(*Mayor)

// Mayor represents a supervisor agent that decomposes high-level tasks into
// subtasks for workers and optionally synthesizes worker results into a final
// response. It works with any configured provider through the router.
type Mayor struct {
	router       *provider.Router
	tracker      *cost.Tracker
	role         string
	systemPrompt string
	maxSubtasks  int
}

const defaultMayorSystemPrompt = `You are a software architect decomposing a task into implementation subtasks for parallel coding workers.

RULES:
- Each subtask is one functional module or component (e.g. "HTTP downloader package", "PostgreSQL schema and migration", "CLI entry point").
- Each subtask must produce complete, working, compilable source code — not setup instructions.
- Name the specific files and Go packages the worker should write.
- Workers run in parallel and cannot see each other's output, so define any shared interfaces inline in the subtask description so workers agree on them.
- Generate as many subtasks as the task genuinely requires (no artificial limit).
- Output ONLY a numbered list of subtasks. No headings, no preamble, no prose.`

// NewMayor creates a Mayor supervisor with the given router and options.
func NewMayor(router *provider.Router, opts ...MayorOption) *Mayor {
	m := &Mayor{
		router:       router,
		role:         "mayor",
		systemPrompt: defaultMayorSystemPrompt,
		maxSubtasks:  10,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// WithMayorRole sets the role name used for router model resolution.
func WithMayorRole(role string) MayorOption {
	return func(m *Mayor) {
		m.role = role
	}
}

// WithMayorSystemPrompt overrides the default system prompt.
func WithMayorSystemPrompt(prompt string) MayorOption {
	return func(m *Mayor) {
		m.systemPrompt = prompt
	}
}

// WithMayorCostTracker attaches a cost tracker for recording token usage.
func WithMayorCostTracker(t *cost.Tracker) MayorOption {
	return func(m *Mayor) {
		m.tracker = t
	}
}

// WithMayorMaxSubtasks sets the maximum number of subtasks returned by Decompose.
func WithMayorMaxSubtasks(n int) MayorOption {
	return func(m *Mayor) {
		m.maxSubtasks = n
	}
}

// Decompose takes a high-level task description and returns a list of discrete
// subtasks for workers. It sends the task to the supervisor model and parses
// the response into individual subtask strings.
func (m *Mayor) Decompose(ctx context.Context, task string) ([]string, error) {
	req := &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: m.systemPrompt},
			{Role: provider.RoleUser, Content: fmt.Sprintf("Decompose this task into subtasks:\n\n%s", task)},
		},
	}

	resp, err := m.router.ChatCompletionForRole(ctx, m.role, req)
	if err != nil {
		return nil, err
	}

	m.recordCost(resp)

	subtasks := ParseSubtasks(resp.Message.Content)
	if len(subtasks) > m.maxSubtasks {
		subtasks = subtasks[:m.maxSubtasks]
	}

	return subtasks, nil
}

// Synthesize takes a set of worker results and produces a unified final response.
// It sends the original task and all worker outputs to the supervisor model,
// which combines them into a coherent synthesis.
func (m *Mayor) Synthesize(ctx context.Context, task string, results []WorkerResult) (string, error) {
	var sb strings.Builder
	sb.WriteString("Original task: ")
	sb.WriteString(task)
	sb.WriteString("\n\nWorker results:\n")

	for i, r := range results {
		fmt.Fprintf(&sb, "\n--- Worker %d (role: %s, subtask: %s) ---\n%s\n", i+1, r.Role, r.Subtask, r.Response)
	}

	req := &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "You are a technical supervisor. Synthesize the following worker results into a unified, coherent response that addresses the original task. Combine insights, resolve any conflicts, and present a clear final answer."},
			{Role: provider.RoleUser, Content: sb.String()},
		},
	}

	resp, err := m.router.ChatCompletionForRole(ctx, m.role, req)
	if err != nil {
		return "", err
	}

	m.recordCost(resp)

	return resp.Message.Content, nil
}

// Plan takes a task and returns both a plan summary and discrete subtasks.
// The model is asked to produce a structured response with a summary section
// and a subtasks section.
func (m *Mayor) Plan(ctx context.Context, task string) (*PlanResult, error) {
	req := &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: m.systemPrompt},
			{Role: provider.RoleUser, Content: fmt.Sprintf("Create a plan for this task. Start with a '## Summary' section explaining the approach, then a '## Subtasks' section with a numbered list of subtasks.\n\n%s", task)},
		},
	}

	resp, err := m.router.ChatCompletionForRole(ctx, m.role, req)
	if err != nil {
		return nil, err
	}

	m.recordCost(resp)

	result := parsePlanResponse(resp.Message.Content)
	if len(result.Subtasks) > m.maxSubtasks {
		result.Subtasks = result.Subtasks[:m.maxSubtasks]
	}

	return result, nil
}

// ParseSubtasks extracts subtask strings from model output text.
// It handles numbered lists ("1. item", "1) item"), dash bullets ("- item"),
// asterisk bullets ("* item"), and unicode bullets ("bullet item").
func ParseSubtasks(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	var subtasks []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		matches := listItemPattern.FindStringSubmatch(line)
		if matches != nil && len(matches) > 1 {
			item := strings.TrimSpace(matches[1])
			// Strip **bold** and *italic* markdown emphasis.
			item = boldPattern.ReplaceAllString(item, "$1")
			item = strings.TrimSpace(item)
			if item != "" {
				subtasks = append(subtasks, item)
			}
		}
	}
	return subtasks
}

// AssessResult holds the output of Mayor.Assess.
type AssessResult struct {
	FetchURLs     []string
	StalenessRisk string // "high", "medium", or "low"
}

const assessSystemPrompt = `You are a technical knowledge assessor. Evaluate whether local AI workers would have accurate, up-to-date knowledge to complete the given task.

Local workers have training data with an approximate cutoff of 2024-01. Respond with ONLY valid JSON — no prose, no markdown fences, no explanation:

{"staleness_risk": "high|medium|low", "fetch_urls": ["url1", "url2"]}

Rules:
- staleness_risk: "high" if the task involves software, APIs, or practices released or significantly changed after 2024-01; "medium" if possibly outdated; "low" if stable or timeless
- fetch_urls: list 0-3 specific official documentation URLs workers need for accurate output. Only include URLs you are highly confident currently exist and are relevant. Use empty array if not needed.
- Prefer official documentation (docs.example.com, pkg.go.dev, github.com/*/README, etc.)`

// Assess evaluates a task for training-data staleness and returns URLs to fetch.
// If the mayor cannot identify relevant URLs or the risk is low, FetchURLs will be empty.
// A failed assess call is non-fatal — callers should warn and continue.
func (m *Mayor) Assess(ctx context.Context, task string) (*AssessResult, error) {
	userMsg := fmt.Sprintf(
		"Today's date is %s.\n\nTask: %s\n\nRespond with ONLY valid JSON:\n{\"staleness_risk\": \"high|medium|low\", \"fetch_urls\": [\"url1\"]}",
		time.Now().Format("2006-01-02"), task,
	)
	req := &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: assessSystemPrompt},
			{Role: provider.RoleUser, Content: userMsg},
		},
	}

	resp, err := m.router.ChatCompletionForRole(ctx, m.role, req)
	if err != nil {
		return nil, err
	}

	m.recordCost(resp)

	return ParseAssessResult(resp.Message.Content), nil
}

// ParseAssessResult extracts an AssessResult from a model response.
// It handles prose-wrapped JSON and markdown code fences.
// On malformed or absent JSON it returns an empty AssessResult (not an error).
func ParseAssessResult(text string) *AssessResult {
	// Strip common markdown code fences.
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	// Find the JSON object between the first { and last }.
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return &AssessResult{}
	}

	var raw struct {
		StalenessRisk string   `json:"staleness_risk"`
		FetchURLs     []string `json:"fetch_urls"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return &AssessResult{}
	}
	return &AssessResult{
		FetchURLs:     raw.FetchURLs,
		StalenessRisk: raw.StalenessRisk,
	}
}

// recordCost records token usage with the cost tracker if one is configured.
func (m *Mayor) recordCost(resp *provider.ChatResponse) {
	if m.tracker == nil || resp == nil {
		return
	}
	m.tracker.Record(
		"",
		resp.Model,
		m.role,
		cost.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	)
}

// parsePlanResponse splits a structured plan response into summary and subtasks.
func parsePlanResponse(text string) *PlanResult {
	result := &PlanResult{}

	// Split on ## headers to find sections.
	sections := strings.Split(text, "## ")
	for _, section := range sections {
		lower := strings.ToLower(section)
		if strings.HasPrefix(lower, "summary") {
			lines := strings.SplitN(section, "\n", 2)
			if len(lines) > 1 {
				result.Summary = strings.TrimSpace(lines[1])
			}
		} else if strings.HasPrefix(lower, "subtask") {
			lines := strings.SplitN(section, "\n", 2)
			if len(lines) > 1 {
				result.Subtasks = ParseSubtasks(lines[1])
			}
		}
	}

	// If no structured sections found, treat the whole text as summary
	// and try to parse subtasks from it.
	if result.Summary == "" && len(result.Subtasks) == 0 {
		result.Summary = strings.TrimSpace(text)
		result.Subtasks = ParseSubtasks(text)
	}

	return result
}
