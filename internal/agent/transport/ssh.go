package transport

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// defaultDialTimeout is the TCP connection timeout for SSH.
const defaultDialTimeout = 15 * time.Second

// SSHManager manages a pool of SSH connections to remote hosts.
// It reuses connections to the same host:port across multiple agent invocations.
type SSHManager struct {
	mu    sync.Mutex
	pool  map[string]*ssh.Client // keyed by "host:port"
}

// NewSSHManager creates a new SSH connection manager.
func NewSSHManager() *SSHManager {
	return &SSHManager{
		pool: make(map[string]*ssh.Client),
	}
}

// GetConnection returns a cached or new SSH connection for the given host.
// Connections are cached by host:port and reused for multiplexing.
func (m *SSHManager) GetConnection(host string, port int, authCfg AuthConfig) (*ssh.Client, error) {
	authCfg.Resolve()
	if port == 0 {
		port = authCfg.Port
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	m.mu.Lock()
	if client, ok := m.pool[addr]; ok {
		// Verify connection is still alive.
		_, _, err := client.SendRequest("keepalive@electrictown", true, nil)
		if err == nil {
			m.mu.Unlock()
			return client, nil
		}
		// Stale connection — remove and reconnect.
		client.Close()
		delete(m.pool, addr)
	}
	m.mu.Unlock()

	// Build auth methods.
	methods, err := BuildAuthMethods(authCfg)
	if err != nil {
		return nil, fmt.Errorf("ssh auth for %s: %w", addr, err)
	}

	// Dial new connection.
	config := &ssh.ClientConfig{
		User:            authCfg.User,
		Auth:            methods,
		// TODO: implement known_hosts checking. For now, accept all hosts
		// (acceptable for lab/internal network use).
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         defaultDialTimeout,
	}

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	// Cache the connection.
	m.mu.Lock()
	m.pool[addr] = client
	m.mu.Unlock()

	return client, nil
}

// NewSession creates a new SSH session on the given client.
// Multiple sessions can be opened on the same client (multiplexing).
func (m *SSHManager) NewSession(client *ssh.Client) (*ssh.Session, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh new session: %w", err)
	}
	return session, nil
}

// Close closes all pooled SSH connections.
func (m *SSHManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for addr, client := range m.pool {
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.pool, addr)
	}
	return firstErr
}
