package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ambuj14sept/pssh/pkg/config"
	"github.com/ambuj14sept/pssh/pkg/daemon"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create directories: %v\n", err)
		os.Exit(1)
	}

	server := daemon.NewServer(cfg.SocketPath)

	if err := server.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("psshd started on %s\n", cfg.SocketPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh

	fmt.Println("\nShutting down...")
	server.Stop()
}
