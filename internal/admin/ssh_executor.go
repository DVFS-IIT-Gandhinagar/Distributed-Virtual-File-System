package admin

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHExecutor defines the interface for running commands on remote nodes via SSH.
type SSHExecutor interface {
	Run(ctx context.Context, host string, port int, user string, keyPath string, command string, stdout io.Writer, stderr io.Writer) (int, error)
}

// RemoteSSHExecutor executes commands on remote Linux servers using public key SSH authentication.
type RemoteSSHExecutor struct{}

// NewRemoteSSHExecutor creates a new RemoteSSHExecutor.
func NewRemoteSSHExecutor() *RemoteSSHExecutor {
	return &RemoteSSHExecutor{}
}

// expandPath expands leading ~ to user's home directory.
func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimPrefix(path, "~")
		trimmed = strings.TrimPrefix(trimmed, "/")
		trimmed = strings.TrimPrefix(trimmed, "\\")
		return filepath.Join(home, trimmed), nil
	}
	return path, nil
}

// Run connects to host:port via SSH and executes command, streaming stdout and stderr.
func (r *RemoteSSHExecutor) Run(ctx context.Context, host string, port int, user string, keyPath string, command string, stdout io.Writer, stderr io.Writer) (int, error) {
	if user == "" {
		return -1, fmt.Errorf("SSH user cannot be empty")
	}
	if keyPath == "" {
		return -1, fmt.Errorf("SSH key path cannot be empty")
	}

	resolvedKeyPath, err := expandPath(keyPath)
	if err != nil {
		return -1, fmt.Errorf("failed to expand key path '%s': %w", keyPath, err)
	}

	keyBytes, err := os.ReadFile(resolvedKeyPath)
	if err != nil {
		return -1, fmt.Errorf("failed to read private key from '%s': %w", resolvedKeyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return -1, fmt.Errorf("failed to parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	if port <= 0 {
		port = 22
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// Dial with context timeout
	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return -1, fmt.Errorf("failed to connect to %s: %w", addr, err)
	}
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		return -1, fmt.Errorf("SSH handshake failed with %s: %w", addr, err)
	}
	defer sshConn.Close()

	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return -1, fmt.Errorf("failed to create SSH session on %s: %w", addr, err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stderr

	// Handle cancellation via context
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = session.Signal(ssh.SIGKILL)
			_ = session.Close()
		case <-done:
		}
	}()

	err = session.Run(command)
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			if strings.Contains(command, "reboot") && (exitErr.ExitStatus() == 255 || exitErr.ExitStatus() == -1) {
				return 0, nil
			}
			return exitErr.ExitStatus(), nil
		}
		if strings.Contains(command, "reboot") {
			errMsg := strings.ToLower(err.Error())
			if err == io.EOF || strings.Contains(errMsg, "eof") || strings.Contains(errMsg, "reset") || strings.Contains(errMsg, "closed") {
				return 0, nil
			}
		}
		return -1, err
	}

	return 0, nil
}

// MockSSHResponse defines a simulated execution result for testing.
type MockSSHResponse struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Delay    time.Duration
	Err      error
}

// MockSSHExecutor simulates SSH command execution for unit and integration testing.
type MockSSHExecutor struct {
	mu        sync.RWMutex
	Responses map[string]MockSSHResponse
	Default   MockSSHResponse
	Calls     []MockSSHCall
}

// MockSSHCall records details of an executed call.
type MockSSHCall struct {
	Host    string
	Port    int
	User    string
	KeyPath string
	Command string
}

// NewMockSSHExecutor creates a new MockSSHExecutor with nominal default response.
func NewMockSSHExecutor() *MockSSHExecutor {
	return &MockSSHExecutor{
		Responses: make(map[string]MockSSHResponse),
		Default: MockSSHResponse{
			Stdout:   "OK\n",
			ExitCode: 0,
		},
	}
}

// Run simulates command execution using registered responses.
func (m *MockSSHExecutor) Run(ctx context.Context, host string, port int, user string, keyPath string, command string, stdout io.Writer, stderr io.Writer) (int, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, MockSSHCall{
		Host:    host,
		Port:    port,
		User:    user,
		KeyPath: keyPath,
		Command: command,
	})

	resp, ok := m.Responses[command]
	if !ok {
		resp = m.Default
	}
	m.mu.Unlock()

	if resp.Delay > 0 {
		select {
		case <-time.After(resp.Delay):
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}

	if resp.Stdout != "" && stdout != nil {
		_, _ = stdout.Write([]byte(resp.Stdout))
	}
	if resp.Stderr != "" && stderr != nil {
		_, _ = stderr.Write([]byte(resp.Stderr))
	}

	return resp.ExitCode, resp.Err
}
