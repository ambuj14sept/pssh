package client

import (
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type SSHAgent struct {
	agent.Agent
	conn net.Conn
}

func NewSSHAgent() (*SSHAgent, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, os.ErrNotExist
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}

	return &SSHAgent{
		Agent: agent.NewClient(conn),
		conn:  conn,
	}, nil
}

func (a *SSHAgent) Auth() []ssh.AuthMethod {
	return []ssh.AuthMethod{ssh.PublicKeysCallback(a.Signers)}
}

func (a *SSHAgent) Close() error {
	return a.conn.Close()
}
