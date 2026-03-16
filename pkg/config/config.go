package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	AppName    = "pssh"
	Version    = "1.0.0"
	DaemonName = "psshd"
)

type Config struct {
	HomeDir     string
	PsshDir     string
	SessionsDir string
	SocketPath  string
	DaemonPath  string
	SSHOpts     SSHOptions
}

type SSHOptions struct {
	ServerAliveInterval int
	ServerAliveCountMax int
	ConnectTimeout      int
	KeepAlive           bool
}

func DefaultSSHOptions() SSHOptions {
	return SSHOptions{
		ServerAliveInterval: 10,
		ServerAliveCountMax: 3,
		ConnectTimeout:      10,
		KeepAlive:           true,
	}
}

func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	psshDir := filepath.Join(home, ".pssh")

	return &Config{
		HomeDir:     home,
		PsshDir:     psshDir,
		SessionsDir: filepath.Join(psshDir, "sessions"),
		SocketPath:  filepath.Join(psshDir, "psshd.sock"),
		DaemonPath:  filepath.Join(psshDir, DaemonName),
		SSHOpts:     DefaultSSHOptions(),
	}, nil
}

func (c *Config) EnsureDirs() error {
	for _, dir := range []string{c.PsshDir, c.SessionsDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

func GenerateSessionID() string {
	return fmt.Sprintf("pssh_%d_%d", time.Now().Unix(), os.Getpid())
}
