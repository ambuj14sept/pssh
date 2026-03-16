package daemon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/ambuj14sept/pssh/pkg/protocol"
	"github.com/creack/pty"
)

type Session struct {
	ID        string
	Command   []string
	CreatedAt time.Time
	PTY       *os.File
	Cmd       *exec.Cmd
	Attached  bool
	Mutex     sync.RWMutex
	clients   map[chan []byte]bool
	clientsMu sync.RWMutex
	exitCode  int
	exited    bool
	cols      uint16
	rows      uint16
}

func NewSession(id string, command []string, cols, rows uint16, termType string, clientEnv map[string]string) (*Session, error) {
	session := &Session{
		ID:        id,
		Command:   command,
		CreatedAt: time.Now(),
		clients:   make(map[chan []byte]bool),
		cols:      cols,
		rows:      rows,
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	var cmd *exec.Cmd
	if len(command) > 0 {
		cmd = exec.Command(command[0], command[1:]...)
	} else {
		// Start as a login shell by setting argv[0] to "-shellname"
		cmd = exec.Command(shell)
		cmd.Args[0] = "-" + filepath.Base(shell)
	}

	// Build a proper environment like a real SSH session
	currentUser, _ := user.Current()
	homeDir := os.Getenv("HOME")
	if homeDir == "" && currentUser != nil {
		homeDir = currentUser.HomeDir
	}
	username := os.Getenv("USER")
	if username == "" && currentUser != nil {
		username = currentUser.Username
	}

	if termType == "" {
		termType = "xterm-256color"
	}

	env := []string{
		fmt.Sprintf("HOME=%s", homeDir),
		fmt.Sprintf("USER=%s", username),
		fmt.Sprintf("LOGNAME=%s", username),
		fmt.Sprintf("SHELL=%s", shell),
		fmt.Sprintf("TERM=%s", termType),
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}

	// Preserve LANG/locale from daemon environment if not overridden by client
	for _, key := range []string{"LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE"} {
		if val, ok := clientEnv[key]; ok {
			env = append(env, fmt.Sprintf("%s=%s", key, val))
		} else if val := os.Getenv(key); val != "" {
			env = append(env, fmt.Sprintf("%s=%s", key, val))
		}
	}

	// Add any additional client environment variables
	for key, val := range clientEnv {
		// Skip ones we already handled
		if key == "LANG" || key == "LANGUAGE" || key == "LC_ALL" || key == "LC_CTYPE" {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", key, val))
	}

	cmd.Env = env
	cmd.Dir = homeDir

	ptyFile, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start pty: %w", err)
	}

	session.PTY = ptyFile
	session.Cmd = cmd

	if err := pty.Setsize(ptyFile, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		return nil, fmt.Errorf("failed to set terminal size: %w", err)
	}

	go session.handleProcessExit()
	go session.readLoop()

	return session, nil
}

func (s *Session) handleProcessExit() {
	err := s.Cmd.Wait()
	s.Mutex.Lock()
	s.exited = true
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				s.exitCode = status.ExitStatus()
			} else {
				s.exitCode = 1
			}
		} else {
			s.exitCode = 1
		}
	}
	s.Mutex.Unlock()

	s.broadcastExit()
}

func (s *Session) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.PTY.Read(buf)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "Session %s read error: %v\n", s.ID, err)
			}
			return
		}
		if n > 0 {
			s.broadcast(buf[:n])
		}
	}
}

func (s *Session) broadcast(data []byte) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for ch := range s.clients {
		select {
		case ch <- data:
		default:
		}
	}
}

func (s *Session) broadcastExit() {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for ch := range s.clients {
		close(ch)
	}
}

func (s *Session) Attach(clientCh chan []byte) error {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()

	if s.exited {
		return fmt.Errorf("session has exited")
	}

	s.clientsMu.Lock()
	s.clients[clientCh] = true
	s.clientsMu.Unlock()

	s.Attached = true

	return nil
}

func (s *Session) Detach(clientCh chan []byte) {
	s.clientsMu.Lock()
	delete(s.clients, clientCh)
	s.clientsMu.Unlock()

	s.Mutex.Lock()
	s.Attached = len(s.clients) > 0
	s.Mutex.Unlock()
}

func (s *Session) Write(data []byte) error {
	s.Mutex.RLock()
	defer s.Mutex.RUnlock()

	if s.exited {
		return fmt.Errorf("session has exited")
	}

	_, err := s.PTY.Write(data)
	return err
}

func (s *Session) Resize(cols, rows uint16) error {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()

	s.cols = cols
	s.rows = rows

	return pty.Setsize(s.PTY, &pty.Winsize{Cols: cols, Rows: rows})
}

func (s *Session) Kill() error {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()

	if s.exited {
		return nil
	}

	return s.Cmd.Process.Kill()
}

func (s *Session) GetInfo() protocol.SessionInfo {
	s.Mutex.RLock()
	defer s.Mutex.RUnlock()

	cmd := "shell"
	if len(s.Command) > 0 {
		cmd = s.Command[0]
	}

	pid := 0
	if s.Cmd.Process != nil {
		pid = s.Cmd.Process.Pid
	}

	return protocol.SessionInfo{
		SessionID: s.ID,
		Command:   cmd,
		CreatedAt: s.CreatedAt,
		Attached:  s.Attached,
		PID:       pid,
	}
}

func (s *Session) IsExited() bool {
	s.Mutex.RLock()
	defer s.Mutex.RUnlock()
	return s.exited
}

func (s *Session) ExitCode() int {
	s.Mutex.RLock()
	defer s.Mutex.RUnlock()
	return s.exitCode
}
