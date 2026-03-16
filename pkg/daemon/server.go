package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/ambuj14sept/pssh/pkg/protocol"
)

type Server struct {
	socketPath string
	sessions   map[string]*Session
	sessionsMu sync.RWMutex
	listener   net.Listener
	quit       chan bool
}

func NewServer(socketPath string) *Server {
	return &Server{
		socketPath: socketPath,
		sessions:   make(map[string]*Session),
		quit:       make(chan bool),
	}
}

func (s *Server) Start() error {
	os.Remove(s.socketPath)

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}

	s.listener = listener

	if err := os.Chmod(s.socketPath, 0600); err != nil {
		return fmt.Errorf("failed to chmod socket: %w", err)
	}

	go s.acceptLoop()

	return nil
}

func (s *Server) Stop() error {
	close(s.quit)

	if s.listener != nil {
		s.listener.Close()
	}

	s.sessionsMu.Lock()
	for _, session := range s.sessions {
		session.Kill()
	}
	s.sessionsMu.Unlock()

	os.Remove(s.socketPath)

	return nil
}

func (s *Server) acceptLoop() {
	for {
		select {
		case <-s.quit:
			return
		default:
		}

		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return
			default:
				fmt.Fprintf(os.Stderr, "Accept error: %v\n", err)
				continue
			}
		}

		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var msg protocol.Message
		if err := decoder.Decode(&msg); err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "Decode error: %v\n", err)
			}
			return
		}

		switch msg.Type {
		case protocol.MessageTypeHello:
			s.handleHello(conn, encoder, &msg)

		case protocol.MessageTypeCreateSession:
			s.handleCreateSession(conn, encoder, decoder, &msg)

		case protocol.MessageTypeAttachSession:
			s.handleAttachSession(conn, encoder, decoder, &msg)

		case protocol.MessageTypeListSessions:
			s.handleListSessions(encoder)

		case protocol.MessageTypeKillSession:
			s.handleKillSession(encoder, &msg)

		default:
			s.sendError(encoder, fmt.Sprintf("unknown message type: %s", msg.Type))
		}
	}
}

func (s *Server) handleHello(conn net.Conn, encoder *json.Encoder, msg *protocol.Message) {
	var payload protocol.HelloPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		s.sendError(encoder, "invalid hello payload")
		return
	}

	s.sendSuccess(encoder, "hello", nil)
}

func (s *Server) handleCreateSession(conn net.Conn, encoder *json.Encoder, decoder *json.Decoder, msg *protocol.Message) {
	var req protocol.CreateSessionRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		s.sendError(encoder, "invalid create session payload")
		return
	}

	sessionID := generateSessionID()

	session, err := NewSession(sessionID, req.Command, req.Cols, req.Rows, req.Term, req.Env)
	if err != nil {
		resp := protocol.CreateSessionResponse{
			Success: false,
			Error:   err.Error(),
		}
		s.sendSuccess(encoder, protocol.MessageTypeCreateSession, resp)
		return
	}

	s.sessionsMu.Lock()
	s.sessions[sessionID] = session
	s.sessionsMu.Unlock()

	resp := protocol.CreateSessionResponse{
		SessionID: sessionID,
		Success:   true,
	}
	s.sendSuccess(encoder, protocol.MessageTypeCreateSession, resp)

	s.handleSessionIO(conn, encoder, decoder, session)
}

func (s *Server) handleAttachSession(conn net.Conn, encoder *json.Encoder, decoder *json.Decoder, msg *protocol.Message) {
	var req protocol.AttachSessionRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		s.sendError(encoder, "invalid attach session payload")
		return
	}

	s.sessionsMu.RLock()
	session, exists := s.sessions[req.SessionID]
	s.sessionsMu.RUnlock()

	if !exists {
		s.sendError(encoder, fmt.Sprintf("session not found: %s", req.SessionID))
		return
	}

	if session.IsExited() {
		s.sessionsMu.Lock()
		delete(s.sessions, req.SessionID)
		s.sessionsMu.Unlock()
		s.sendError(encoder, "session has exited")
		return
	}

	session.Resize(req.Cols, req.Rows)
	s.sendSuccess(encoder, protocol.MessageTypeAttachSession, nil)
	s.handleSessionIO(conn, encoder, decoder, session)
}

func (s *Server) handleSessionIO(conn net.Conn, encoder *json.Encoder, decoder *json.Decoder, session *Session) {
	dataCh := make(chan []byte, 100)

	if err := session.Attach(dataCh); err != nil {
		s.sendError(encoder, err.Error())
		return
	}
	defer session.Detach(dataCh)

	doneCh := make(chan struct{})

	// Read PTY output and send to client; detect session exit when channel is closed
	go func() {
		defer close(doneCh)
		for data := range dataCh {
			msg, _ := protocol.NewMessage(protocol.MessageTypeData, &protocol.DataPayload{Data: data})
			encoder.Encode(msg)
		}
		// Channel was closed — session has exited
		exitCode := session.ExitCode()
		exitMsg, _ := protocol.NewMessage(protocol.MessageTypeExit, &protocol.ExitPayload{ExitCode: exitCode})
		encoder.Encode(exitMsg)
	}()

	// Read client input and forward to PTY
	go func() {
		for {
			var msg protocol.Message
			if err := decoder.Decode(&msg); err != nil {
				if err != io.EOF {
					fmt.Fprintf(os.Stderr, "Session decode error: %v\n", err)
				}
				return
			}

			switch msg.Type {
			case protocol.MessageTypeData:
				var payload protocol.DataPayload
				if err := json.Unmarshal(msg.Payload, &payload); err != nil {
					continue
				}
				session.Write(payload.Data)

			case protocol.MessageTypeResize:
				var payload protocol.ResizePayload
				if err := json.Unmarshal(msg.Payload, &payload); err != nil {
					continue
				}
				session.Resize(payload.Cols, payload.Rows)

			case protocol.MessageTypeExit:
				return
			}
		}
	}()

	// Wait for session exit (dataCh closed)
	<-doneCh
}

func (s *Server) handleListSessions(encoder *json.Encoder) {
	s.sessionsMu.RLock()
	sessions := make([]protocol.SessionInfo, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session.GetInfo())
	}
	s.sessionsMu.RUnlock()

	resp := protocol.ListSessionsResponse{Sessions: sessions}
	s.sendSuccess(encoder, protocol.MessageTypeListSessions, resp)
}

func (s *Server) handleKillSession(encoder *json.Encoder, msg *protocol.Message) {
	var req protocol.KillSessionRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		s.sendError(encoder, "invalid kill session payload")
		return
	}

	if req.KillAll {
		s.sessionsMu.Lock()
		for id, session := range s.sessions {
			session.Kill()
			delete(s.sessions, id)
		}
		s.sessionsMu.Unlock()
	} else {
		s.sessionsMu.Lock()
		session, exists := s.sessions[req.SessionID]
		if exists {
			session.Kill()
			delete(s.sessions, req.SessionID)
		}
		s.sessionsMu.Unlock()

		if !exists {
			s.sendError(encoder, fmt.Sprintf("session not found: %s", req.SessionID))
			return
		}
	}

	s.sendSuccess(encoder, protocol.MessageTypeKillSession, nil)
}

func (s *Server) sendSuccess(encoder *json.Encoder, msgType string, payload interface{}) {
	msg, _ := protocol.NewMessage(msgType, payload)
	encoder.Encode(msg)
}

func (s *Server) sendError(encoder *json.Encoder, err string) {
	msg, _ := protocol.NewMessage(protocol.MessageTypeError, &protocol.ErrorPayload{Error: err})
	encoder.Encode(msg)
}

func generateSessionID() string {
	return fmt.Sprintf("pssh_%d_%d", time.Now().Unix(), os.Getpid())
}
