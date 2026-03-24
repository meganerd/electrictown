package agent

import (
	"fmt"
	"os/exec"
)

// HealthStatus describes the availability of a configured agent.
type HealthStatus struct {
	Name      string
	AgentType string
	Transport string
	Host      string
	Available bool
	Detail    string // human-readable status detail
}

// CheckLocal verifies that the agent CLI binary is available locally.
// It checks for the binary via exec.LookPath.
func CheckLocal(name, command string) HealthStatus {
	status := HealthStatus{
		Name:      name,
		Transport: "local",
	}
	path, err := exec.LookPath(command)
	if err != nil {
		status.Available = false
		status.Detail = fmt.Sprintf("binary %q not found in PATH", command)
		return status
	}
	status.Available = true
	status.Detail = path
	return status
}

// DefaultCommand returns the default CLI binary name for an agent type.
func DefaultCommand(agentType string) string {
	switch agentType {
	case "claude-code":
		return "claude"
	case "codex":
		return "codex"
	case "aider":
		return "aider"
	case "opencode":
		return "opencode"
	case "openclaw":
		return "openclaw"
	case "gemini-cli":
		return "gemini"
	case "cursor":
		return "cursor"
	case "skill":
		return "" // skill type requires explicit command in config
	default:
		return agentType
	}
}
