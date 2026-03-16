package client

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type SSHClient struct {
	config *ssh.ClientConfig
	addr   string
	user   string
}

func NewSSHClient(user, host string, port int, sshOpts map[string]string) (*SSHClient, error) {
	config := &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	if keyPath, ok := sshOpts["identityfile"]; ok {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("unable to read private key: %v", err)
		}

		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("unable to parse private key: %v", err)
		}

		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	} else {
		if sshAgent, err := NewSSHAgent(); err == nil {
			config.Auth = append(config.Auth, sshAgent.Auth()...)
		}

		homeDir, _ := os.UserHomeDir()
		defaultKeys := []string{
			filepath.Join(homeDir, ".ssh", "id_rsa"),
			filepath.Join(homeDir, ".ssh", "id_ed25519"),
		}

		for _, keyPath := range defaultKeys {
			if _, err := os.Stat(keyPath); err == nil {
				key, err := os.ReadFile(keyPath)
				if err != nil {
					continue
				}

				signer, err := ssh.ParsePrivateKey(key)
				if err != nil {
					continue
				}

				config.Auth = append(config.Auth, ssh.PublicKeys(signer))
			}
		}
	}

	if password, ok := sshOpts["password"]; ok {
		config.Auth = append(config.Auth, ssh.Password(password))
	}

	if len(config.Auth) == 0 {
		return nil, fmt.Errorf("no authentication method available")
	}

	if timeout, ok := sshOpts["connecttimeout"]; ok {
		config.Timeout = time.Duration(parseDuration(timeout)) * time.Second
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	return &SSHClient{
		config: config,
		addr:   addr,
		user:   user,
	}, nil
}

func (c *SSHClient) Connect() (*ssh.Client, error) {
	client, err := ssh.Dial("tcp", c.addr, c.config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}
	return client, nil
}

func (c *SSHClient) ExecuteCommand(client *ssh.Client, cmd string) (string, string, int, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	err = session.Run(cmd)
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			return "", "", 0, err
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, nil
}

func (c *SSHClient) DeployDaemon(client *ssh.Client, daemonBinary []byte) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	remotePath := fmt.Sprintf("/home/%s/.pssh/psshd", c.user)
	rmCmd := fmt.Sprintf("rm -f %s", remotePath)
	c.ExecuteCommand(client, rmCmd)

	mkdirCmd := fmt.Sprintf("mkdir -p /home/%s/.pssh", c.user)
	_, _, _, err = c.ExecuteCommand(client, mkdirCmd)
	if err != nil {
		return fmt.Errorf("failed to create remote directory: %w", err)
	}

	go func() {
		stdin.Write(daemonBinary)
		stdin.Close()
	}()

	scpCmd := fmt.Sprintf("cat > %s", remotePath)
	err = session.Run(scpCmd)
	if err != nil {
		return fmt.Errorf("failed to write daemon binary: %w", err)
	}

	chmodCmd := fmt.Sprintf("chmod +x %s", remotePath)
	_, _, _, err = c.ExecuteCommand(client, chmodCmd)
	if err != nil {
		return fmt.Errorf("failed to chmod daemon binary: %w", err)
	}

	return nil
}

func (c *SSHClient) StartDaemon(client *ssh.Client) error {
	daemonPath := fmt.Sprintf("/home/%s/.pssh/psshd", c.user)

	checkCmd := fmt.Sprintf("pgrep -f '%s' > /dev/null 2>&1 && echo 'running' || echo 'stopped'", daemonPath)
	stdout, _, _, err := c.ExecuteCommand(client, checkCmd)
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if strings.TrimSpace(stdout) == "running" {
		return nil
	}

	startCmd := fmt.Sprintf("nohup %s > /dev/null 2>&1 &", daemonPath)
	_, _, _, err = c.ExecuteCommand(client, startCmd)
	if err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	return nil
}

func parseDuration(s string) int {
	return 10
}
