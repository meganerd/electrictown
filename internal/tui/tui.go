// Package tui provides a Bubble Tea terminal UI for electrictown.
// It subscribes to the event bus and renders DAG progress, worker status,
// and cost/time information in real time.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/meganerd/electrictown/internal/event"
)

// styles holds all TUI styling.
var styles = struct {
	header    lipgloss.Style
	phase     lipgloss.Style
	success   lipgloss.Style
	failure   lipgloss.Style
	warning   lipgloss.Style
	muted     lipgloss.Style
	footer    lipgloss.Style
	separator lipgloss.Style
	helpKey   lipgloss.Style
	helpDesc  lipgloss.Style
	inputBox  lipgloss.Style
	timeline  lipgloss.Style
}{
	header:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),
	phase:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213")),
	success:   lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
	failure:   lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
	warning:   lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
	muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	footer:    lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
	separator: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	helpKey:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),
	helpDesc:  lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	inputBox:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39")).Padding(0, 1),
	timeline:  lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
}

// viewMode tracks which view the TUI is showing.
type viewMode int

const (
	modeInput     viewMode = iota // Waiting for user to type task.
	modeExecution                 // Task running, showing progress.
)

// eventMsg wraps an event.Event for the Bubble Tea message loop.
type eventMsg event.Event

// tickMsg triggers periodic UI refresh.
type tickMsg time.Time

// taskSubmittedMsg is sent when the user submits a task from the input view.
type taskSubmittedMsg string

// maxLogLines caps the log pane size to prevent unbounded memory growth.
const maxLogLines = 100

// Model is the Bubble Tea model for the electrictown TUI.
type Model struct {
	// Configuration.
	sub    event.Subscriber
	width  int
	height int
	mode   viewMode

	// Input state.
	textarea   textarea.Model
	configPath string
	configInfo string // summary of config (models, pool size, etc.)

	// Run state.
	version   string
	task      string
	logDir    string
	startTime time.Time

	// Phase tracking.
	currentPhase string
	phases       []phaseRecord

	// Subtask/DAG state.
	subtasks []subtaskState
	hasDeps  bool

	// Worker state.
	workers []workerState

	// Cost tracking.
	totalTokens int
	cost        float64

	// Log pane.
	logLines []string

	// Completion.
	done     bool
	duration time.Duration

	// Task submission callback (set by RunInteractive).
	onSubmit func(string)
}

type phaseRecord struct {
	name     string
	duration time.Duration
}

type subtaskState struct {
	description string
	specialist  string
	model       string
	status      string // "pending", "running", "done", "failed"
}

type workerState struct {
	index    int
	model    string
	status   string // "idle", "running", "done", "failed", "retrying"
	content  string // latest content chunk
	tokens   int
	tps      float64
	duration time.Duration
	score    int
	flagged  bool
}

// New creates a TUI model that reads from the given event subscriber.
// If task is empty, the TUI starts in input mode.
func New(sub event.Subscriber, task, configPath, configInfo, ver string) Model {
	ta := textarea.New()
	ta.Placeholder = "Describe the task for electrictown workers..."
	ta.CharLimit = 4096
	ta.SetWidth(60)
	ta.SetHeight(3)
	ta.Focus()

	mode := modeExecution
	if task == "" {
		mode = modeInput
	}

	return Model{
		sub:        sub,
		startTime:  time.Now(),
		width:      80,
		height:     24,
		mode:       mode,
		textarea:   ta,
		task:       task,
		configPath: configPath,
		configInfo: configInfo,
		version:    ver,
	}
}

// Init starts the event listener and tick timer.
func (m Model) Init() tea.Cmd {
	if m.mode == modeInput {
		return tea.Batch(textarea.Blink, m.tick())
	}
	return tea.Batch(
		m.listenForEvents(),
		m.tick(),
	)
}

// Update handles Bubble Tea messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.mode == modeInput {
			return m.updateInput(msg)
		}
		return m.updateExecution(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textarea.SetWidth(min(m.width-6, 80))

	case tickMsg:
		if m.done {
			return m, nil
		}
		return m, m.tick()

	case eventMsg:
		e := event.Event(msg)
		m.handleEvent(e)
		m.appendLog(e)
		if m.done {
			// Let user see final state before quitting.
			return m, nil
		}
		return m, m.listenForEvents()

	case taskSubmittedMsg:
		m.task = string(msg)
		m.mode = modeExecution
		m.startTime = time.Now()
		if m.onSubmit != nil {
			m.onSubmit(m.task)
		}
		return m, m.listenForEvents()
	}

	// Forward to textarea in input mode.
	if m.mode == modeInput {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyEnter:
		task := strings.TrimSpace(m.textarea.Value())
		if task == "" {
			return m, nil // Don't submit empty task.
		}
		return m, func() tea.Msg { return taskSubmittedMsg(task) }
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m Model) updateExecution(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// View renders the TUI.
func (m Model) View() string {
	if m.mode == modeInput {
		return m.viewInput()
	}
	return m.viewExecution()
}

func (m Model) viewInput() string {
	var b strings.Builder

	// Header.
	b.WriteString(styles.header.Render(fmt.Sprintf("⚡ electrictown %s", m.version)))
	b.WriteString("\n")
	b.WriteString(styles.separator.Render(strings.Repeat("─", min(m.width, 60))))
	b.WriteString("\n\n")

	// Config info.
	b.WriteString(styles.muted.Render(fmt.Sprintf("  Config: %s", m.configPath)))
	b.WriteString("\n")
	if m.configInfo != "" {
		b.WriteString(styles.muted.Render(fmt.Sprintf("  %s", m.configInfo)))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Task input prompt.
	b.WriteString("  " + styles.phase.Render("Task:") + "\n")
	b.WriteString(styles.inputBox.Render(m.textarea.View()))
	b.WriteString("\n\n")

	// Help bar.
	b.WriteString(m.renderHelpBar())

	return b.String()
}

func (m Model) viewExecution() string {
	var b strings.Builder

	// Header zone.
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// Phase timeline (completed phases).
	if len(m.phases) > 0 {
		b.WriteString(m.renderTimeline())
	}

	// Active phase.
	if m.currentPhase != "" {
		b.WriteString(styles.phase.Render("▶ "+m.currentPhase) + "\n")
	}

	// DAG progress zone.
	if len(m.subtasks) > 0 {
		b.WriteString(m.renderDAG())
		b.WriteString("\n")
	}

	// Worker panel zone.
	if len(m.workers) > 0 {
		b.WriteString(m.renderWorkers())
		b.WriteString("\n")
	}

	// Log pane.
	if len(m.logLines) > 0 {
		b.WriteString(m.renderLogPane())
		b.WriteString("\n")
	}

	// Footer zone (cost + time + help).
	b.WriteString(m.renderFooter())
	b.WriteString("\n")
	b.WriteString(m.renderHelpBar())

	return b.String()
}

// --- Event handling ---

func (m *Model) handleEvent(e event.Event) {
	switch e.Type {
	case event.TaskStarted:
		if d, ok := e.Data.(event.TaskStartedData); ok {
			m.version = d.Version
			m.task = d.Task
			m.configPath = d.ConfigPath
			m.logDir = d.LogDir
			m.startTime = e.Timestamp
		}

	case event.PhaseStarted:
		if d, ok := e.Data.(event.PhaseData); ok {
			m.currentPhase = d.Name
		}

	case event.PhaseCompleted:
		if d, ok := e.Data.(event.PhaseData); ok {
			m.phases = append(m.phases, phaseRecord{name: d.Name, duration: d.Duration})
			m.currentPhase = ""
		}

	case event.SubtaskDecomposed:
		if d, ok := e.Data.(event.SubtaskDecomposedData); ok {
			m.hasDeps = d.HasDeps
			m.subtasks = make([]subtaskState, len(d.Subtasks))
			for i, st := range d.Subtasks {
				m.subtasks[i] = subtaskState{
					description: st,
					status:      "pending",
				}
			}
		}

	case event.SpecialistAssigned:
		if d, ok := e.Data.(event.SpecialistAssignedData); ok {
			if d.Index < len(m.subtasks) {
				m.subtasks[d.Index].specialist = d.Specialist
				m.subtasks[d.Index].model = d.Model
			}
		}

	case event.WorkerStarted:
		if d, ok := e.Data.(event.WorkerData); ok {
			m.ensureWorker(d.Index, d.Total)
			m.workers[d.Index].model = d.Model
			m.workers[d.Index].status = "running"
			if d.Index < len(m.subtasks) {
				m.subtasks[d.Index].status = "running"
			}
		}

	case event.WorkerProgress:
		if d, ok := e.Data.(event.WorkerData); ok {
			m.ensureWorker(d.Index, d.Total)
			m.workers[d.Index].content = d.Content
		}

	case event.WorkerCompleted:
		if d, ok := e.Data.(event.WorkerData); ok {
			m.ensureWorker(d.Index, d.Total)
			m.workers[d.Index].status = "done"
			m.workers[d.Index].tokens = d.Tokens
			m.workers[d.Index].tps = d.TPS
			m.workers[d.Index].duration = d.Duration
			m.workers[d.Index].score = d.Score
			m.workers[d.Index].flagged = d.Flagged
			if d.Index < len(m.subtasks) {
				m.subtasks[d.Index].status = "done"
			}
		}

	case event.WorkerFailed:
		if d, ok := e.Data.(event.WorkerData); ok {
			m.ensureWorker(d.Index, d.Total)
			m.workers[d.Index].status = "failed"
			if d.Index < len(m.subtasks) {
				m.subtasks[d.Index].status = "failed"
			}
		}

	case event.GuardrailRetry:
		if d, ok := e.Data.(event.GuardrailRetryData); ok {
			m.ensureWorker(d.Index, 0)
			m.workers[d.Index].status = "retrying"
		}

	case event.CostUpdate:
		if d, ok := e.Data.(event.CostUpdateData); ok {
			m.totalTokens = d.TotalTokens
			m.cost = d.EstimatedCost
		}

	case event.RunCompleted:
		if d, ok := e.Data.(event.RunCompletedData); ok {
			m.done = true
			m.duration = d.Duration
		}
	}
}

func (m *Model) appendLog(e event.Event) {
	line := formatLogLine(e)
	if line == "" {
		return
	}
	m.logLines = append(m.logLines, line)
	if len(m.logLines) > maxLogLines {
		m.logLines = m.logLines[len(m.logLines)-maxLogLines:]
	}
}

func formatLogLine(e event.Event) string {
	ts := e.Timestamp.Format("15:04:05")
	switch e.Type {
	case event.PhaseStarted:
		if d, ok := e.Data.(event.PhaseData); ok {
			return fmt.Sprintf("[%s] %s", ts, d.Name)
		}
	case event.SubtaskDecomposed:
		if d, ok := e.Data.(event.SubtaskDecomposedData); ok {
			return fmt.Sprintf("[%s] Decomposed into %d subtasks", ts, len(d.Subtasks))
		}
	case event.WorkerCompleted:
		if d, ok := e.Data.(event.WorkerData); ok {
			return fmt.Sprintf("[%s] Worker %d done (%d tok, %.1fs)", ts, d.Index+1, d.Tokens, d.Duration.Seconds())
		}
	case event.WorkerFailed:
		if d, ok := e.Data.(event.WorkerData); ok {
			return fmt.Sprintf("[%s] Worker %d FAILED: %s", ts, d.Index+1, d.Error)
		}
	case event.GuardrailRetry:
		if d, ok := e.Data.(event.GuardrailRetryData); ok {
			return fmt.Sprintf("[%s] Guardrail retry worker %d (attempt %d/%d, score %d)", ts, d.Index+1, d.Attempt, d.MaxRetries, d.Score)
		}
	case event.RAGIngest:
		if d, ok := e.Data.(event.RAGData); ok {
			return fmt.Sprintf("[%s] RAG ingested %d chunks", ts, d.Count)
		}
	case event.RAGQuery:
		if d, ok := e.Data.(event.RAGData); ok {
			return fmt.Sprintf("[%s] RAG retrieved %d chunks", ts, d.Count)
		}
	case event.JinaFetch:
		if d, ok := e.Data.(event.JinaFetchData); ok {
			if d.Error != "" {
				return fmt.Sprintf("[%s] Jina fetch failed: %s — %s", ts, d.URL, d.Error)
			}
			return fmt.Sprintf("[%s] Fetched %s (%d chars)", ts, d.URL, d.Length)
		}
	case event.ValidationResult:
		if d, ok := e.Data.(event.ValidationData); ok {
			if !d.Passed {
				return fmt.Sprintf("[%s] Validation failed worker %d", ts, d.Index+1)
			}
		}
	case event.RunCompleted:
		if d, ok := e.Data.(event.RunCompletedData); ok {
			return fmt.Sprintf("[%s] Run completed in %s", ts, d.Duration.Round(time.Millisecond))
		}
	}
	return ""
}

func (m *Model) ensureWorker(idx, total int) {
	needed := idx + 1
	if total > needed {
		needed = total
	}
	for len(m.workers) < needed {
		m.workers = append(m.workers, workerState{
			index:  len(m.workers),
			status: "idle",
		})
	}
}

// --- Rendering ---

func (m Model) renderHeader() string {
	title := styles.header.Render(fmt.Sprintf("⚡ electrictown %s", m.version))
	sep := styles.separator.Render(strings.Repeat("─", min(m.width, 60)))
	task := styles.muted.Render(truncate(m.task, m.width-4))
	return fmt.Sprintf("%s\n%s\n%s", title, sep, task)
}

func (m Model) renderTimeline() string {
	var parts []string
	for _, p := range m.phases {
		dur := ""
		if p.duration > 0 {
			dur = fmt.Sprintf(" (%s)", p.duration.Round(time.Millisecond))
		}
		// Shorten phase name for timeline (strip "Phase X: " detail).
		name := p.name
		if idx := strings.Index(name, ":"); idx > 0 && idx < 12 {
			name = name[:idx]
		}
		parts = append(parts, styles.success.Render("✓")+styles.timeline.Render(name+dur))
	}
	return strings.Join(parts, styles.separator.Render(" → ")) + "\n"
}

func (m Model) renderDAG() string {
	var b strings.Builder
	b.WriteString(styles.separator.Render("── Subtasks ") + styles.separator.Render(strings.Repeat("─", max(0, m.width-14))) + "\n")

	maxShow := m.height / 3
	if maxShow < 4 {
		maxShow = 4
	}
	if maxShow > len(m.subtasks) {
		maxShow = len(m.subtasks)
	}

	for i := 0; i < maxShow; i++ {
		st := m.subtasks[i]
		icon := statusIcon(st.status)
		desc := truncate(st.description, m.width-10)
		if st.specialist != "" {
			desc = fmt.Sprintf("[%s] %s", st.specialist, truncate(st.description, m.width-10-len(st.specialist)-3))
		}
		b.WriteString(fmt.Sprintf(" %s %s\n", icon, desc))
	}

	if len(m.subtasks) > maxShow {
		b.WriteString(styles.muted.Render(fmt.Sprintf(" … and %d more\n", len(m.subtasks)-maxShow)))
	}

	return b.String()
}

func (m Model) renderWorkers() string {
	var b strings.Builder
	b.WriteString(styles.separator.Render("── Workers ") + styles.separator.Render(strings.Repeat("─", max(0, m.width-13))) + "\n")

	for _, w := range m.workers {
		icon := statusIcon(w.status)
		model := truncate(w.model, 20)
		if model == "" {
			model = "…"
		}

		switch w.status {
		case "running":
			preview := truncate(w.content, m.width-30)
			if preview == "" {
				preview = "working…"
			}
			b.WriteString(fmt.Sprintf(" %s [%d] %-20s %s\n", icon, w.index+1, model, styles.muted.Render(preview)))
		case "done":
			tps := ""
			if w.tps > 0 {
				tps = fmt.Sprintf(" %.0ft/s", w.tps)
			}
			score := ""
			if w.score > 0 {
				if w.flagged {
					score = styles.warning.Render(fmt.Sprintf(" ⚑%d/10", w.score))
				} else {
					score = styles.success.Render(fmt.Sprintf(" ✓%d/10", w.score))
				}
			}
			b.WriteString(fmt.Sprintf(" %s [%d] %-20s %dtok%s %.1fs%s\n",
				icon, w.index+1, model, w.tokens, tps, w.duration.Seconds(), score))
		case "failed":
			b.WriteString(fmt.Sprintf(" %s [%d] %-20s %s\n", icon, w.index+1, model, styles.failure.Render("FAILED")))
		case "retrying":
			b.WriteString(fmt.Sprintf(" %s [%d] %-20s %s\n", icon, w.index+1, model, styles.warning.Render("retrying…")))
		default:
			b.WriteString(fmt.Sprintf(" %s [%d] %-20s %s\n", icon, w.index+1, model, styles.muted.Render("idle")))
		}
	}

	return b.String()
}

func (m Model) renderLogPane() string {
	var b strings.Builder
	b.WriteString(styles.separator.Render("── Log ") + styles.separator.Render(strings.Repeat("─", max(0, m.width-9))) + "\n")

	// Show last N lines that fit in available space.
	maxLines := 6
	start := len(m.logLines) - maxLines
	if start < 0 {
		start = 0
	}
	for _, line := range m.logLines[start:] {
		b.WriteString(" " + styles.muted.Render(truncate(line, m.width-3)) + "\n")
	}

	return b.String()
}

func (m Model) renderFooter() string {
	sep := styles.separator.Render(strings.Repeat("─", min(m.width, 60)))
	elapsed := time.Since(m.startTime).Round(time.Second)
	tokens := formatTokens(m.totalTokens)

	left := styles.footer.Render(fmt.Sprintf("⏱ %s", elapsed))
	right := styles.footer.Render(fmt.Sprintf("🪙 %s tokens", tokens))

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}

	return fmt.Sprintf("%s\n%s%s%s", sep, left, strings.Repeat(" ", gap), right)
}

func (m Model) renderHelpBar() string {
	if m.mode == modeInput {
		return styles.helpKey.Render("enter") + styles.helpDesc.Render(" submit") +
			styles.muted.Render(" · ") +
			styles.helpKey.Render("esc") + styles.helpDesc.Render(" quit")
	}
	help := styles.helpKey.Render("q") + styles.helpDesc.Render(" quit")
	if m.done {
		help = styles.helpKey.Render("q") + styles.helpDesc.Render(" exit") +
			styles.muted.Render(" · ") +
			styles.success.Render("✓ run complete")
	}
	return help
}

// --- Helpers ---

func statusIcon(status string) string {
	switch status {
	case "pending":
		return styles.muted.Render("○")
	case "running":
		return styles.phase.Render("●")
	case "done":
		return styles.success.Render("✓")
	case "failed":
		return styles.failure.Render("✗")
	case "retrying":
		return styles.warning.Render("↻")
	default:
		return styles.muted.Render("·")
	}
}

func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1000000)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- Tea commands ---

func (m Model) listenForEvents() tea.Cmd {
	sub := m.sub
	return func() tea.Msg {
		e, ok := <-sub
		if !ok {
			return eventMsg(event.New(event.RunCompleted, event.RunCompletedData{
				Duration: time.Since(m.startTime),
			}))
		}
		return eventMsg(e)
	}
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Run starts the TUI in execution mode (task already known), blocking until
// completion. Called from cmd/et/main.go when --tui is set with a task arg.
func Run(sub event.Subscriber, task, configPath, configInfo, ver string) error {
	p := tea.NewProgram(New(sub, task, configPath, configInfo, ver), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// RunInteractive starts the TUI in input mode (no task yet). The user types a
// task, then onSubmit is called with the task text. The caller should start
// orchestration in the callback. Returns the submitted task or empty if cancelled.
func RunInteractive(sub event.Subscriber, configPath, configInfo, ver string, onSubmit func(string)) (string, error) {
	m := New(sub, "", configPath, configInfo, ver)
	m.onSubmit = onSubmit
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return "", err
	}
	if fm, ok := finalModel.(Model); ok {
		return fm.task, nil
	}
	return "", nil
}
