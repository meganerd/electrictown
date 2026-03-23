package transport

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/meganerd/electrictown/internal/agent"
)

// DiscoveredAgent represents a coding agent found on a remote host.
type DiscoveredAgent struct {
	Type    string // agent type (e.g., "claude-code")
	Command string // binary name
	Version string // version string (if available)
	Found   bool   // whether the binary was found
}

// agentBinaries maps agent types to their CLI binary names.
var agentBinaries = map[string]string{
	"claude-code": "claude",
	"codex":       "codex",
	"aider":       "aider",
	"opencode":    "opencode",
	"openclaw":    "openclaw",
	"gemini-cli":  "gemini",
}

// DiscoverAgents checks which coding agent CLIs are installed on a remote host.
// It runs `which <binary> && <binary> --version` for each known agent type.
func DiscoverAgents(client *ssh.Client, mgr *SSHManager) ([]DiscoveredAgent, error) {
	var results []DiscoveredAgent

	for _, agentType := range agent.ValidAgentTypes() {
		binary := agentBinaries[string(agentType)]
		if binary == "" {
			binary = string(agentType)
		}

		discovered := DiscoveredAgent{
			Type:    string(agentType),
			Command: binary,
		}

		session, err := mgr.NewSession(client)
		if err != nil {
			return nil, fmt.Errorf("creating session for discovery: %w", err)
		}

		// Check if binary exists and try to get version.
		cmd := fmt.Sprintf("which %s 2>/dev/null && %s --version 2>/dev/null || echo 'NOT_FOUND'", binary, binary)
		output, err := session.CombinedOutput(cmd)
		session.Close()

		outputStr := strings.TrimSpace(string(output))
		if err != nil || outputStr == "NOT_FOUND" || outputStr == "" {
			discovered.Found = false
		} else {
			discovered.Found = true
			// Extract version from output (second line if present, else full output).
			lines := strings.Split(outputStr, "\n")
			if len(lines) > 1 {
				discovered.Version = strings.TrimSpace(lines[len(lines)-1])
			} else {
				discovered.Version = outputStr
			}
		}

		results = append(results, discovered)
	}

	return results, nil
}
