package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ambuj14sept/pssh/pkg/client"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	sshOpts  map[string]string
	port     int
	identity string
	verbose  bool
	version  = "1.0.0"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "pssh [ssh-options] [user@]host [-- command]",
		Short: "Persistent SSH with automatic reconnection",
		Long: `pssh is a drop-in SSH replacement that provides VS Code-like session persistence.

Your sessions survive connection drops, laptop sleep, and WiFi changes - automatically.`,
		Version: version,
		Run:     runConnect,
		Args:    cobra.ArbitraryArgs,
	}

	rootCmd.PersistentFlags().IntVarP(&port, "port", "p", 22, "SSH port")
	rootCmd.PersistentFlags().StringVarP(&identity, "identity", "i", "", "SSH identity file")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")

	rootCmd.AddCommand(
		newListCommand(),
		newAttachCommand(),
		newKillCommand(),
		newStatusCommand(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runConnect(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		cmd.Help()
		os.Exit(1)
	}

	target, command := parseArgs(args)
	user, host := parseTarget(target)

	sshOpts := buildSSHOptions()

	printConnecting(host, user)

	sshClient, err := client.NewSSHClient(user, host, port, sshOpts)
	if err != nil {
		printError(fmt.Sprintf("Failed to create SSH client: %v", err))
		os.Exit(1)
	}

	clientConn, err := sshClient.Connect()
	if err != nil {
		printError(fmt.Sprintf("Failed to connect to %s: %v", target, err))
		os.Exit(1)
	}
	defer clientConn.Close()

	if err := client.DeployAndStartDaemon(clientConn, user); err != nil {
		printError(fmt.Sprintf("Failed to deploy daemon: %v", err))
		os.Exit(1)
	}

	session := client.NewSession(clientConn, user)

	var sessionID string
	var cmdStr string
	if len(command) > 0 {
		sessionID, err = session.Create(command)
		cmdStr = strings.Join(command, " ")
	} else {
		sessionID, err = session.Create(nil)
		cmdStr = "shell"
	}
	if err != nil {
		printError(fmt.Sprintf("Failed to create session: %v", err))
		os.Exit(1)
	}

	if err := client.TrackSession(sessionID, target, cmdStr); err != nil {
		printWarn(fmt.Sprintf("Failed to track session locally: %v", err))
	}
	defer client.UntrackSession(sessionID)

	printConnected(sessionID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\n")
		printInfo("Disconnecting...")
		session.Close()
	}()

	exitCode, err := runWithReconnect(session, sshClient, user, host, port, sshOpts, sessionID, command)
	if err != nil {
		printError(fmt.Sprintf("Session error: %v", err))
		os.Exit(1)
	}

	os.Exit(exitCode)
}

func runWithReconnect(session *client.Session, sshClient *client.SSHClient, user, host string, port int, sshOpts map[string]string, sessionID string, command []string) (int, error) {
	maxRetries := 50
	retryDelay := 1 * time.Second
	maxDelay := 30 * time.Second
	retries := 0

	for {
		exitCode, err := session.Run()
		if err == nil && exitCode == 0 {
			return exitCode, nil
		}

		retries++
		if retries >= maxRetries {
			printError("Too many consecutive failures. Giving up.")
			printInfo(fmt.Sprintf("Your session %s is still running on the server.", sessionID))
			printInfo(fmt.Sprintf("Reattach later with: pssh attach %s@%s %s", user, host, sessionID))
			return exitCode, nil
		}

		printWarn(fmt.Sprintf("Connection lost. Reconnecting in %ds... (attempt %d)", int(retryDelay.Seconds()), retries))
		printInfo("Press Enter to retry now or q to quit.")

		if waitForInput(retryDelay) {
			printInfo(fmt.Sprintf("Stopped by user. Session %s is still running on the server.", sessionID))
			printInfo(fmt.Sprintf("Reattach later with: pssh attach %s@%s %s", user, host, sessionID))
			return exitCode, nil
		}

		retryDelay *= 2
		if retryDelay > maxDelay {
			retryDelay = maxDelay
		}

		newClientConn, err := sshClient.Connect()
		if err != nil {
			continue
		}

		err = session.Attach(sessionID)
		if err != nil {
			printError(fmt.Sprintf("Failed to reattach: %v", err))
			newClientConn.Close()
			continue
		}

		retries = 0
		retryDelay = 1 * time.Second
	}
}

func waitForInput(timeout time.Duration) bool {
	ch := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		os.Stdin.Read(buf)
		if buf[0] == 'q' || buf[0] == 'Q' {
			ch <- struct{}{}
		}
	}()

	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list [user@]host",
		Short: "List active sessions on a remote server",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			target := args[0]
			user, host := parseTarget(target)

			sshOpts := buildSSHOptions()

			sshClient, err := client.NewSSHClient(user, host, port, sshOpts)
			if err != nil {
				printError(fmt.Sprintf("Failed to create SSH client: %v", err))
				os.Exit(1)
			}

			clientConn, err := sshClient.Connect()
			if err != nil {
				printError(fmt.Sprintf("Failed to connect: %v", err))
				os.Exit(1)
			}
			defer clientConn.Close()

			manager := client.NewSessionManager(clientConn, user)
			sessions, err := manager.ListSessions()
			if err != nil {
				printError(fmt.Sprintf("Failed to list sessions: %v", err))
				os.Exit(1)
			}

			if len(sessions) == 0 {
				printInfo("No active pssh sessions found.")
				return
			}

			printInfo(fmt.Sprintf("Active sessions on %s:", target))
			fmt.Println()
			for _, s := range sessions {
				status := "no"
				if s.Attached {
					status = "yes"
				}
				fmt.Printf("  %s | created: %s | attached: %s\n",
					color.GreenString(s.SessionID),
					s.CreatedAt.Format("2006-01-02 15:04:05"),
					status,
				)
			}
			fmt.Println()
			printInfo(fmt.Sprintf("Reattach with: pssh attach %s <session_id>", target))
		},
	}
}

func newAttachCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [user@]host <session_id>",
		Short: "Reattach to an existing session",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			target := args[0]
			sessionID := args[1]
			user, host := parseTarget(target)

			sshOpts := buildSSHOptions()

			sshClient, err := client.NewSSHClient(user, host, port, sshOpts)
			if err != nil {
				printError(fmt.Sprintf("Failed to create SSH client: %v", err))
				os.Exit(1)
			}

			clientConn, err := sshClient.Connect()
			if err != nil {
				printError(fmt.Sprintf("Failed to connect: %v", err))
				os.Exit(1)
			}
			defer clientConn.Close()

			session := client.NewSession(clientConn, user)
			if err := session.Attach(sessionID); err != nil {
				printError(fmt.Sprintf("Failed to attach: %v", err))
				os.Exit(1)
			}

			if err := client.TrackSession(sessionID, target, "reattached"); err != nil {
				printWarn(fmt.Sprintf("Failed to track session: %v", err))
			}
			defer client.UntrackSession(sessionID)

			printInfo(fmt.Sprintf("Reattaching to %s...", sessionID))

			exitCode, err := runWithReconnect(session, sshClient, user, host, port, sshOpts, sessionID, nil)
			if err != nil {
				printError(fmt.Sprintf("Session error: %v", err))
				os.Exit(1)
			}

			os.Exit(exitCode)
		},
	}
}

func newKillCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "kill [user@]host <session_id|--all>",
		Short: "Kill a remote session",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			target := args[0]
			sessionArg := args[1]
			user, host := parseTarget(target)

			killAll := sessionArg == "--all"
			sessionID := sessionArg

			sshOpts := buildSSHOptions()

			sshClient, err := client.NewSSHClient(user, host, port, sshOpts)
			if err != nil {
				printError(fmt.Sprintf("Failed to create SSH client: %v", err))
				os.Exit(1)
			}

			clientConn, err := sshClient.Connect()
			if err != nil {
				printError(fmt.Sprintf("Failed to connect: %v", err))
				os.Exit(1)
			}
			defer clientConn.Close()

			manager := client.NewSessionManager(clientConn, user)
			if err := manager.KillSession(sessionID, killAll); err != nil {
				printError(fmt.Sprintf("Failed to kill session: %v", err))
				os.Exit(1)
			}

			if killAll {
				printSuccess("Killed all pssh sessions")
			} else {
				printSuccess(fmt.Sprintf("Killed session %s", sessionID))
			}
		},
	}
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show all tracked sessions from this machine",
		Run: func(cmd *cobra.Command, args []string) {
			sessions, err := client.GetLocalSessions()
			if err != nil {
				printError(fmt.Sprintf("Failed to get sessions: %v", err))
				os.Exit(1)
			}

			if len(sessions) == 0 {
				printInfo("No active pssh sessions from this machine.")
				return
			}

			printInfo("Sessions from this machine:")
			fmt.Println()
			for _, s := range sessions {
				statusIcon := "○"
				if s["status"] == "running" {
					statusIcon = color.GreenString("●")
				}

				fmt.Printf("  %s %s\n", statusIcon, color.New(color.Bold).Sprint(s["session_id"]))
				fmt.Printf("    Server:  %s\n", s["target"])
				fmt.Printf("    Command: %s\n", s["command"])
				fmt.Printf("    Started: %s\n", s["started"])
				fmt.Println()
			}
		},
	}
}

func parseArgs(args []string) (target string, command []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[0], args[i+1:]
		}
	}
	return args[0], nil
}

func parseTarget(target string) (user, host string) {
	parts := strings.SplitN(target, "@", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return os.Getenv("USER"), target
}

func buildSSHOptions() map[string]string {
	opts := make(map[string]string)
	if identity != "" {
		opts["identityfile"] = identity
	}
	return opts
}

func printConnecting(host, user string) {
	fmt.Printf("%s Connecting to %s%s%s as %s%s%s\n",
		color.CyanString("[pssh]"),
		color.New(color.Bold).Sprint(host),
		color.New(color.Faint).Sprint(""),
		color.New(color.Faint).Sprint(""),
		color.New(color.Bold).Sprint(user),
		color.New(color.Faint).Sprint(""),
	)
}

func printConnected(sessionID string) {
	fmt.Printf("%s Session %s%s%s established. Type exit to end.\n",
		color.CyanString("[pssh]"),
		color.New(color.Bold).Sprint(sessionID),
		color.New(color.Faint).Sprint(""),
	)
	fmt.Println()
}

func printInfo(msg string) {
	fmt.Printf("%s %s\n", color.CyanString("[pssh]"), msg)
}

func printSuccess(msg string) {
	fmt.Printf("%s %s\n", color.GreenString("[pssh]"), msg)
}

func printWarn(msg string) {
	fmt.Printf("%s %s\n", color.YellowString("[pssh]"), msg)
}

func printError(msg string) {
	fmt.Printf("%s %s\n", color.RedString("[pssh]"), msg)
}

func init() {
	sshOpts = make(map[string]string)
}
