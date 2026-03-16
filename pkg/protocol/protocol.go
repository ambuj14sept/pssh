package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const (
	Version = "1.0.0"

	MessageTypeHello         = "hello"
	MessageTypeCreateSession = "create_session"
	MessageTypeAttachSession = "attach_session"
	MessageTypeKillSession   = "kill_session"
	MessageTypeListSessions  = "list_sessions"
	MessageTypeData          = "data"
	MessageTypeResize        = "resize"
	MessageTypeError         = "error"
	MessageTypeSuccess       = "success"
	MessageTypeSessionInfo   = "session_info"
	MessageTypeExit          = "exit"
)

type Message struct {
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	SessionID string          `json:"session_id,omitempty"`
}

type HelloPayload struct {
	Version  string `json:"version"`
	ClientID string `json:"client_id"`
}

type CreateSessionRequest struct {
	Command []string `json:"command,omitempty"`
	Cols    uint16   `json:"cols"`
	Rows    uint16   `json:"rows"`
}

type CreateSessionResponse struct {
	SessionID string `json:"session_id"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

type AttachSessionRequest struct {
	SessionID string `json:"session_id"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

type KillSessionRequest struct {
	SessionID string `json:"session_id"`
	KillAll   bool   `json:"kill_all"`
}

type ListSessionsRequest struct{}

type SessionInfo struct {
	SessionID string    `json:"session_id"`
	Command   string    `json:"command"`
	CreatedAt time.Time `json:"created_at"`
	Attached  bool      `json:"attached"`
	PID       int       `json:"pid"`
}

type ListSessionsResponse struct {
	Sessions []SessionInfo `json:"sessions"`
}

type DataPayload struct {
	Data []byte `json:"data"`
}

type ResizePayload struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type ErrorPayload struct {
	Error string `json:"error"`
}

type ExitPayload struct {
	ExitCode int `json:"exit_code"`
}

func ReadMessage(r io.Reader) (*Message, error) {
	decoder := json.NewDecoder(r)
	var msg Message
	if err := decoder.Decode(&msg); err != nil {
		return nil, fmt.Errorf("failed to decode message: %w", err)
	}
	return &msg, nil
}

func WriteMessage(w io.Writer, msg *Message) error {
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(msg); err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}
	return nil
}

func NewMessage(msgType string, payload interface{}) (*Message, error) {
	var rawPayload json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		rawPayload = data
	}

	return &Message{
		Type:      msgType,
		Payload:   rawPayload,
		Timestamp: time.Now(),
	}, nil
}
