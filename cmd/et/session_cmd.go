package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/meganerd/electrictown/internal/provider"
	"github.com/meganerd/electrictown/internal/session"
	"github.com/meganerd/electrictown/internal/tmux"
)

// cmdSession implements the "et session" subcommand with spawn/list/attach/kill/send.
func cmdSession(args []string) error {
	if len(args) < 1 {
		printSessionUsage()
		return nil
	}

	subcmd := args[0]
	switch subcmd {
	case "spawn":
		return cmdSessionSpawn(args[1:])
	case "list", "ls":
		return cmdSessionList(args[1:])
	case "attach":
		return cmdSessionAttach(args[1:])
	case "kill":
		return cmdSessionKill(args[1:])
	case "send":
		return cmdSessionSend(args[1:])
	case "--help", "-h", "help":
		printSessionUsage()
		return nil
	default:
		return fmt.Errorf("unknown session command: %s\n\n%s", subcmd, sessionUsageText())
	}
}

func printSessionUsage() {
	fmt.Fprint(os.Stderr, sessionUsageText())
}

func sessionUsageText() string {
	return `et session - Manage interactive agent sessions in tmux

Usage:
  et session spawn [--role name] [--dir path] [--config path] "prompt"
  et session list
  et session attach <session-name>
  et session kill <session-name>
  et session send <session-name> "text"

Commands:
  spawn    Create a new tmux session for an agent
  list     List active et-* tmux sessions
  attach   Attach to a tmux session
  kill     Kill a tmux session
  send     Send text input to a tmux session
`
}

// cmdSessionSpawn creates a new tmux session for an agent.
func cmdSessionSpawn(args []string) error {
	fs := flag.NewFlagSet("session spawn", flag.ExitOnError)
	role := fs.String("role", "polecat", "agent role name")
	workDir := fs.String("dir", ".", "working directory")
	configPath := fs.String("config", "electrictown.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return fmt.Errorf("prompt required\n\nUsage: et session spawn [--role name] [--dir path] \"prompt\"")
	}

	// Load config to build the command via the adapter.
	cfg, err := provider.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	adapter := session.NewElectrictownAdapter(cfg, *configPath)

	// Resolve session config.
	sessCfg, err := adapter.ResolveConfig(*role)
	if err != nil {
		return fmt.Errorf("resolve config for role %q: %w", *role, err)
	}
	sessCfg.WorkDir = *workDir

	// Build the command.
	cmdName, cmdArgs := adapter.BuildCommand(sessCfg, prompt)
	fullCmd := cmdName
	if len(cmdArgs) > 0 {
		fullCmd = cmdName + " " + strings.Join(cmdArgs, " ")
	}

	// Generate session name.
	runner := tmux.NewAutoRunner()
	shortID, err := generateShortID()
	if err != nil {
		return fmt.Errorf("generate session ID: %w", err)
	}
	sessionName := fmt.Sprintf("et-%s-%s", *role, shortID)

	// Create the tmux session.
	if err := runner.NewSession(sessionName, fullCmd, *workDir); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	fmt.Printf("Created session: %s\n", sessionName)
	fmt.Printf("  Role:    %s\n", *role)
	fmt.Printf("  Dir:     %s\n", *workDir)
	fmt.Printf("  Prompt:  %s\n", truncate(prompt, 80))
	fmt.Printf("\nAttach with: et session attach %s\n", sessionName)
	return nil
}

// cmdSessionList lists active et-* tmux sessions.
func cmdSessionList(_ []string) error {
	runner := tmux.NewAutoRunner()

	sessions, err := runner.ListSessions()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	var etSessions []string
	for _, name := range sessions {
		if strings.HasPrefix(name, "et-") {
			etSessions = append(etSessions, name)
		}
	}

	if len(etSessions) == 0 {
		fmt.Println("No active et sessions.")
		return nil
	}

	fmt.Printf("%-30s\n", "SESSION NAME")
	fmt.Printf("%-30s\n", "------------")
	for _, name := range etSessions {
		fmt.Printf("%-30s\n", name)
	}
	return nil
}

// cmdSessionAttach attaches to a tmux session.
func cmdSessionAttach(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("session name required\n\nUsage: et session attach <session-name>")
	}

	name := args[0]

	// Use exec.Command directly to attach (needs interactive terminal).
	cmd := exec.Command("tmux", "attach-session", "-t", name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cmdSessionKill kills a tmux session.
func cmdSessionKill(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("session name required\n\nUsage: et session kill <session-name>")
	}

	name := args[0]
	runner := tmux.NewAutoRunner()

	if err := runner.KillSession(name); err != nil {
		return fmt.Errorf("kill session %q: %w", name, err)
	}

	fmt.Printf("Killed session: %s\n", name)
	return nil
}

// cmdSessionSend sends text to a tmux session.
func cmdSessionSend(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("session name and text required\n\nUsage: et session send <session-name> \"text\"")
	}

	name := args[0]
	text := strings.Join(args[1:], " ")

	runner := tmux.NewAutoRunner()

	if err := runner.SendKeys(name, text); err != nil {
		return fmt.Errorf("send to session %q: %w", name, err)
	}

	fmt.Printf("Sent to %s: %s\n", name, truncate(text, 80))
	return nil
}

// generateShortID produces a 4-character hex string for session naming.
func generateShortID() (string, error) {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
