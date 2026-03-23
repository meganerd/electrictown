// Package transport provides SSH transport for remote agent execution.
package transport

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	sshagent "golang.org/x/crypto/ssh/agent"
)

// defaultKeyNames is the ordered list of SSH key files to try.
var defaultKeyNames = []string{
	"id_ed25519",
	"id_ecdsa",
	"id_rsa",
}

// AuthConfig holds SSH authentication parameters extracted from agent config.
type AuthConfig struct {
	User    string // SSH username (default: current user)
	Port    int    // SSH port (default: 22)
	KeyPath string // explicit path to SSH private key (optional)
}

// Resolve fills in defaults for empty fields.
func (a *AuthConfig) Resolve() {
	if a.User == "" {
		if u, err := user.Current(); err == nil {
			a.User = u.Username
		}
	}
	if a.Port == 0 {
		a.Port = 22
	}
}

// BuildAuthMethods returns SSH auth methods based on the config.
// It tries, in order: explicit key, ssh-agent, default keys.
func BuildAuthMethods(cfg AuthConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// 1. Explicit key path.
	if cfg.KeyPath != "" {
		signer, err := loadKey(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("loading explicit SSH key %s: %w", cfg.KeyPath, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	// 2. SSH agent.
	if agentAuth, err := agentAuthMethod(); err == nil && agentAuth != nil {
		methods = append(methods, agentAuth)
	}

	// 3. Default key files (~/.ssh/id_ed25519, id_ecdsa, id_rsa).
	if cfg.KeyPath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			for _, name := range defaultKeyNames {
				keyPath := filepath.Join(home, ".ssh", name)
				signer, err := loadKey(keyPath)
				if err != nil {
					continue // key doesn't exist or can't be read
				}
				methods = append(methods, ssh.PublicKeys(signer))
				break // use first found default key
			}
		}
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH auth methods available (no keys found, no ssh-agent)")
	}
	return methods, nil
}

// loadKey reads and parses a private key file.
func loadKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("parsing key %s: %w", path, err)
	}
	return signer, nil
}

// agentAuthMethod returns an ssh.AuthMethod using the running ssh-agent,
// or nil if SSH_AUTH_SOCK is not set.
func agentAuthMethod() (ssh.AuthMethod, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("connecting to ssh-agent: %w", err)
	}
	// Note: we don't close conn here — it needs to stay open for the
	// duration of the SSH connection. The GC will handle it when the
	// agent client is no longer referenced.
	agentClient := sshagent.NewClient(conn)
	return ssh.PublicKeysCallback(agentClient.Signers), nil
}
