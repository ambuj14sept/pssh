package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ambuj14sept/pssh/pkg/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

type Session struct {
	client     *ssh.Client
	sessionID  string
	target     string
	isAttached bool
	oldState   *term.State
	conn       net.Conn
	encoder    *json.Encoder
	decoder    *json.Decoder
}

func NewSession(client *ssh.Client, target string) *Session {
	return &Session{
		client: client,
		target: target,
	}
}

func (s *Session) Create(command []string) (string, error) {
	sockPath := fmt.Sprintf("/home/%s/.pssh/psshd.sock", s.target)

	conn, err := s.dialUnixSocket(sockPath)
	if err != nil {
		return "", fmt.Errorf("failed to connect to daemon: %w", err)
	}

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	cols, rows := getTerminalSize()

	// Forward TERM and locale settings to the remote session
	termVal := os.Getenv("TERM")
	if termVal == "" {
		termVal = "xterm-256color"
	}

	envVars := make(map[string]string)
	for _, key := range []string{"LANG", "LC_ALL", "LC_CTYPE", "COLORTERM"} {
		if val := os.Getenv(key); val != "" {
			envVars[key] = val
		}
	}

	req := protocol.CreateSessionRequest{
		Command: command,
		Cols:    cols,
		Rows:    rows,
		Term:    termVal,
		Env:     envVars,
	}

	hello, _ := protocol.NewMessage(protocol.MessageTypeHello, &protocol.HelloPayload{
		Version:  protocol.Version,
		ClientID: fmt.Sprintf("pssh-client-%d", os.Getpid()),
	})
	if err := encoder.Encode(hello); err != nil {
		return "", fmt.Errorf("failed to send hello: %w", err)
	}

	// Read hello ack
	var helloResp protocol.Message
	if err := decoder.Decode(&helloResp); err != nil {
		return "", fmt.Errorf("failed to read hello response: %w", err)
	}
	if helloResp.Type == protocol.MessageTypeError {
		var errPayload protocol.ErrorPayload
		json.Unmarshal(helloResp.Payload, &errPayload)
		return "", fmt.Errorf("hello rejected: %s", errPayload.Error)
	}

	msg, _ := protocol.NewMessage(protocol.MessageTypeCreateSession, req)
	if err := encoder.Encode(msg); err != nil {
		return "", fmt.Errorf("failed to send create session: %w", err)
	}

	var resp protocol.Message
	if err := decoder.Decode(&resp); err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.Type == protocol.MessageTypeError {
		var errPayload protocol.ErrorPayload
		json.Unmarshal(resp.Payload, &errPayload)
		return "", fmt.Errorf("failed to create session: %s", errPayload.Error)
	}

	var createResp protocol.CreateSessionResponse
	if err := json.Unmarshal(resp.Payload, &createResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if !createResp.Success {
		return "", fmt.Errorf("failed to create session: %s", createResp.Error)
	}

	s.sessionID = createResp.SessionID
	s.isAttached = true
	s.conn = conn
	s.encoder = encoder
	s.decoder = decoder

	return createResp.SessionID, nil
}

func (s *Session) Attach(sessionID string) error {
	s.sessionID = sessionID
	sockPath := fmt.Sprintf("/home/%s/.pssh/psshd.sock", s.target)

	// Close any existing connection from a previous session
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}

	conn, err := s.dialUnixSocket(sockPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	cols, rows := getTerminalSize()

	req := protocol.AttachSessionRequest{
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}

	hello, _ := protocol.NewMessage(protocol.MessageTypeHello, &protocol.HelloPayload{
		Version:  protocol.Version,
		ClientID: fmt.Sprintf("pssh-client-%d", os.Getpid()),
	})
	if err := encoder.Encode(hello); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send hello: %w", err)
	}

	// Read hello ack
	var helloResp protocol.Message
	if err := decoder.Decode(&helloResp); err != nil {
		conn.Close()
		return fmt.Errorf("failed to read hello response: %w", err)
	}
	if helloResp.Type == protocol.MessageTypeError {
		var errPayload protocol.ErrorPayload
		json.Unmarshal(helloResp.Payload, &errPayload)
		conn.Close()
		return fmt.Errorf("hello rejected: %s", errPayload.Error)
	}

	msg, _ := protocol.NewMessage(protocol.MessageTypeAttachSession, req)
	if err := encoder.Encode(msg); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send attach session: %w", err)
	}

	var resp protocol.Message
	if err := decoder.Decode(&resp); err != nil {
		conn.Close()
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.Type == protocol.MessageTypeError {
		var errPayload protocol.ErrorPayload
		json.Unmarshal(resp.Payload, &errPayload)
		conn.Close()
		return fmt.Errorf("failed to attach session: %s", errPayload.Error)
	}

	s.isAttached = true
	s.conn = conn
	s.encoder = encoder
	s.decoder = decoder

	return nil
}

func (s *Session) Run() (int, error) {
	if s.conn == nil || s.encoder == nil || s.decoder == nil {
		return 1, fmt.Errorf("no active connection; call Create() or Attach() first")
	}

	defer func() {
		s.conn.Close()
		s.conn = nil
		s.encoder = nil
		s.decoder = nil
	}()

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return s.runNonInteractive(s.encoder, s.decoder)
	}

	return s.runInteractive(s.encoder, s.decoder)
}

func (s *Session) runInteractive(encoder *json.Encoder, decoder *json.Decoder) (int, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return 1, fmt.Errorf("failed to set raw mode: %w", err)
	}
	s.oldState = oldState
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	resizeCh := make(chan os.Signal, 1)
	signal.Notify(resizeCh, syscall.SIGWINCH)
	go s.handleResize(encoder, resizeCh)

	errCh := make(chan error, 2)
	exitCh := make(chan int, 1)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				errCh <- err
				return
			}
			if n > 0 {
				msg, _ := protocol.NewMessage(protocol.MessageTypeData, &protocol.DataPayload{Data: buf[:n]})
				if err := encoder.Encode(msg); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	go func() {
		for {
			var msg protocol.Message
			if err := decoder.Decode(&msg); err != nil {
				if err != io.EOF {
					errCh <- err
				}
				return
			}

			switch msg.Type {
			case protocol.MessageTypeData:
				var payload protocol.DataPayload
				if err := json.Unmarshal(msg.Payload, &payload); err != nil {
					continue
				}
				os.Stdout.Write(payload.Data)

			case protocol.MessageTypeExit:
				var payload protocol.ExitPayload
				if err := json.Unmarshal(msg.Payload, &payload); err != nil {
					exitCh <- 0
				} else {
					exitCh <- payload.ExitCode
				}
				return

			case protocol.MessageTypeError:
				var payload protocol.ErrorPayload
				if err := json.Unmarshal(msg.Payload, &payload); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %s\n", payload.Error)
				}
				exitCh <- 1
				return
			}
		}
	}()

	select {
	case code := <-exitCh:
		return code, nil
	case err := <-errCh:
		return 1, err
	}
}

func (s *Session) runNonInteractive(encoder *json.Encoder, decoder *json.Decoder) (int, error) {
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Split(bufio.ScanBytes)
		for scanner.Scan() {
			data := scanner.Bytes()
			msg, _ := protocol.NewMessage(protocol.MessageTypeData, &protocol.DataPayload{Data: data})
			encoder.Encode(msg)
		}
	}()

	for {
		var msg protocol.Message
		if err := decoder.Decode(&msg); err != nil {
			if err != io.EOF {
				return 1, err
			}
			return 0, nil
		}

		switch msg.Type {
		case protocol.MessageTypeData:
			var payload protocol.DataPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				continue
			}
			os.Stdout.Write(payload.Data)

		case protocol.MessageTypeExit:
			var payload protocol.ExitPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return 0, nil
			}
			return payload.ExitCode, nil

		case protocol.MessageTypeError:
			var payload protocol.ErrorPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", payload.Error)
			}
			return 1, nil
		}
	}
}

func (s *Session) handleResize(encoder *json.Encoder, resizeCh chan os.Signal) {
	for range resizeCh {
		cols, rows := getTerminalSize()
		msg, _ := protocol.NewMessage(protocol.MessageTypeResize, &protocol.ResizePayload{
			Cols: cols,
			Rows: rows,
		})
		encoder.Encode(msg)
	}
}

func (s *Session) dialUnixSocket(sockPath string) (net.Conn, error) {
	session, err := s.client.NewSession()
	if err != nil {
		return nil, err
	}
	// NOTE: Do NOT defer session.Close() here. The session's lifetime is
	// managed by the returned sshUnixSocket, whose Close() method handles
	// closing the session. Closing it here would kill the "nc -U" process
	// immediately, making the returned connection dead (EOF on first I/O).

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, err
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, err
	}

	err = session.Start(fmt.Sprintf("nc -U %s", sockPath))
	if err != nil {
		session.Close()
		return nil, err
	}

	return &sshUnixSocket{
		stdin:   stdin,
		stdout:  &readCloserWrapper{stdout},
		session: session,
	}, nil
}

func (s *Session) UpdateClient(client *ssh.Client) {
	s.client = client
}

func (s *Session) Close() {
	if s.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), s.oldState)
	}
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	if s.client != nil {
		s.client.Close()
	}
}

type readCloserWrapper struct {
	io.Reader
}

func (r *readCloserWrapper) Close() error {
	return nil
}

type sshUnixSocket struct {
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	session *ssh.Session
}

func (s *sshUnixSocket) Read(p []byte) (n int, err error) {
	return s.stdout.Read(p)
}

func (s *sshUnixSocket) Write(p []byte) (n int, err error) {
	return s.stdin.Write(p)
}

func (s *sshUnixSocket) Close() error {
	s.stdin.Close()
	s.stdout.Close()
	s.session.Close()
	return nil
}

func (s *sshUnixSocket) LocalAddr() net.Addr                { return nil }
func (s *sshUnixSocket) RemoteAddr() net.Addr               { return nil }
func (s *sshUnixSocket) SetDeadline(t time.Time) error      { return nil }
func (s *sshUnixSocket) SetReadDeadline(t time.Time) error  { return nil }
func (s *sshUnixSocket) SetWriteDeadline(t time.Time) error { return nil }

func getTerminalSize() (uint16, uint16) {
	width, height, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return 80, 24
	}
	return uint16(width), uint16(height)
}
