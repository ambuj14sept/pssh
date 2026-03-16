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
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	cols, rows := getTerminalSize()

	req := protocol.CreateSessionRequest{
		Command: command,
		Cols:    cols,
		Rows:    rows,
	}

	hello, _ := protocol.NewMessage(protocol.MessageTypeHello, &protocol.HelloPayload{
		Version:  protocol.Version,
		ClientID: fmt.Sprintf("pssh-client-%d", os.Getpid()),
	})
	if err := encoder.Encode(hello); err != nil {
		return "", fmt.Errorf("failed to send hello: %w", err)
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

	return createResp.SessionID, nil
}

func (s *Session) Attach(sessionID string) error {
	s.sessionID = sessionID
	sockPath := fmt.Sprintf("/home/%s/.pssh/psshd.sock", s.target)

	conn, err := s.dialUnixSocket(sockPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer conn.Close()

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
		return fmt.Errorf("failed to send hello: %w", err)
	}

	msg, _ := protocol.NewMessage(protocol.MessageTypeAttachSession, req)
	if err := encoder.Encode(msg); err != nil {
		return fmt.Errorf("failed to send attach session: %w", err)
	}

	var resp protocol.Message
	if err := decoder.Decode(&resp); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.Type == protocol.MessageTypeError {
		var errPayload protocol.ErrorPayload
		json.Unmarshal(resp.Payload, &errPayload)
		return fmt.Errorf("failed to attach session: %s", errPayload.Error)
	}

	s.isAttached = true

	return nil
}

func (s *Session) Run() (int, error) {
	sockPath := fmt.Sprintf("/home/%s/.pssh/psshd.sock", s.target)

	conn, err := s.dialUnixSocket(sockPath)
	if err != nil {
		return 1, fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return s.runNonInteractive(encoder, decoder)
	}

	return s.runInteractive(encoder, decoder)
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

func (s *Session) Close() {
	if s.oldState != nil {
		term.Restore(int(os.Stdin.Fd()), s.oldState)
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
