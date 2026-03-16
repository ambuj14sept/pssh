package client

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/ambuj14sept/pssh/pkg/protocol"
	"golang.org/x/crypto/ssh"
)

type SessionManager struct {
	client *ssh.Client
	user   string
}

func NewSessionManager(client *ssh.Client, user string) *SessionManager {
	return &SessionManager{
		client: client,
		user:   user,
	}
}

func (sm *SessionManager) ListSessions() ([]protocol.SessionInfo, error) {
	sockPath := fmt.Sprintf("/home/%s/.pssh/psshd.sock", sm.user)

	conn, err := sm.dialDaemon(sockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	hello, _ := protocol.NewMessage(protocol.MessageTypeHello, &protocol.HelloPayload{
		Version:  protocol.Version,
		ClientID: fmt.Sprintf("pssh-client-%d", os.Getpid()),
	})
	if err := encoder.Encode(hello); err != nil {
		return nil, err
	}

	msg, _ := protocol.NewMessage(protocol.MessageTypeListSessions, &protocol.ListSessionsRequest{})
	if err := encoder.Encode(msg); err != nil {
		return nil, err
	}

	var resp protocol.Message
	if err := decoder.Decode(&resp); err != nil {
		return nil, err
	}

	if resp.Type == protocol.MessageTypeError {
		var errPayload protocol.ErrorPayload
		json.Unmarshal(resp.Payload, &errPayload)
		return nil, fmt.Errorf(errPayload.Error)
	}

	var listResp protocol.ListSessionsResponse
	if err := json.Unmarshal(resp.Payload, &listResp); err != nil {
		return nil, err
	}

	return listResp.Sessions, nil
}

func (sm *SessionManager) KillSession(sessionID string, killAll bool) error {
	sockPath := fmt.Sprintf("/home/%s/.pssh/psshd.sock", sm.user)

	conn, err := sm.dialDaemon(sockPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	hello, _ := protocol.NewMessage(protocol.MessageTypeHello, &protocol.HelloPayload{
		Version:  protocol.Version,
		ClientID: fmt.Sprintf("pssh-client-%d", os.Getpid()),
	})
	if err := encoder.Encode(hello); err != nil {
		return err
	}

	req := protocol.KillSessionRequest{
		SessionID: sessionID,
		KillAll:   killAll,
	}

	msg, _ := protocol.NewMessage(protocol.MessageTypeKillSession, req)
	if err := encoder.Encode(msg); err != nil {
		return err
	}

	var resp protocol.Message
	if err := decoder.Decode(&resp); err != nil {
		return err
	}

	if resp.Type == protocol.MessageTypeError {
		var errPayload protocol.ErrorPayload
		json.Unmarshal(resp.Payload, &errPayload)
		return fmt.Errorf(errPayload.Error)
	}

	return nil
}

func (sm *SessionManager) dialDaemon(sockPath string) (net.Conn, error) {
	session, err := sm.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}

	err = session.Start(fmt.Sprintf("nc -U %s", sockPath))
	if err != nil {
		return nil, err
	}

	return &sshUnixSocket{
		stdin:   stdin,
		stdout:  &readCloserWrapper{stdout},
		session: session,
	}, nil
}

func TrackSession(sessionID, target, cmd string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	sessionsDir := filepath.Join(homeDir, ".pssh", "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		return err
	}

	sessionFile := filepath.Join(sessionsDir, sessionID)
	content := fmt.Sprintf("target=%s\ncommand=%s\nstarted=%s\npid=%d\n",
		target,
		cmd,
		time.Now().UTC().Format(time.RFC3339),
		os.Getpid(),
	)

	return os.WriteFile(sessionFile, []byte(content), 0600)
}

func UntrackSession(sessionID string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	sessionFile := filepath.Join(homeDir, ".pssh", "sessions", sessionID)
	return os.Remove(sessionFile)
}

func GetLocalSessions() ([]map[string]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	sessionsDir := filepath.Join(homeDir, ".pssh", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]string{}, nil
		}
		return nil, err
	}

	var sessions []map[string]string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		data, err := os.ReadFile(filepath.Join(sessionsDir, entry.Name()))
		if err != nil {
			continue
		}

		session := parseSessionFile(string(data))
		session["session_id"] = entry.Name()

		if pid, ok := session["pid"]; ok {
			if isProcessRunning(pid) {
				session["status"] = "running"
			} else {
				session["status"] = "stopped"
				os.Remove(filepath.Join(sessionsDir, entry.Name()))
			}
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

func parseSessionFile(content string) map[string]string {
	session := make(map[string]string)
	var key, value string
	var inKey bool = true

	for i := 0; i < len(content); i++ {
		ch := content[i]
		if ch == '=' {
			inKey = false
		} else if ch == '\n' {
			session[key] = value
			key = ""
			value = ""
			inKey = true
		} else if inKey {
			key += string(ch)
		} else {
			value += string(ch)
		}
	}

	return session
}

func isProcessRunning(pidStr string) bool {
	var pid int
	fmt.Sscanf(pidStr, "%d", &pid)
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(os.Signal(nil))
	return err == nil
}
