// Package tui provides a Bubble Tea terminal UI for electrictown.
// It subscribes to the event bus and renders DAG progress, worker status,
// and cost/time information in real time.
package tui

import (
	"fmt"
	"strings"
	"time"

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
}{
	header:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),
	phase:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213")),
	success:   lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
	failure:   lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
	warning:   lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
	muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	footer:    lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
	separator: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
}

// eventMsg wraps an event.Event for the Bubble Tea message loop.
type eventMsg event.Event

// tickMsg triggers periodic UI refresh.
type tickMsg time.Time

// Model is the Bubble Tea model for the electrictown TUI.
type Model struct {
	// Configuration.
	sub    event.Subscriber
	width  int
	height int

	// Run state.
	version    string
	task       string
	configPath string
	logDir     string
	startTime  time.Time

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

	// Completion.
	done     bool
	duration time.Duration
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
func New(sub event.Subscriber) Model {
	return Model{
		sub:       sub,
		startTime: time.Now(),
		width:     80,
		height:    24,
	}
}

// Init starts the event listener and tick timer.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.listenForEvents(),
		m.tick(),
	)
}

// Update handles Bubble Tea messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		if m.done {
			return m, nil
		}
		return m, m.tick()

	case eventMsg:
		e := event.Event(msg)
		m.handleEvent(e)
		if m.done {
			return m, tea.Quit
		}
		return m, m.listenForEvents()
	}

	return m, nil
}

// View renders the TUI.
func (m Model) View() string {
	var b strings.Builder

	// Header zone.
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// Phase zone.
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

	// Footer zone (cost + time).
	b.WriteString(m.renderFooter())

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

// Run starts the TUI, blocking until completion. This is the main entry
// point called from cmd/et/main.go when --tui is set.
func Run(sub event.Subscriber) error {
	p := tea.NewProgram(New(sub), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
