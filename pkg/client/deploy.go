package client

import (
	_ "embed"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

var (
	//go:embed psshd_linux_amd64
	daemonBinaryAmd64 []byte

	//go:embed psshd_linux_arm64
	daemonBinaryArm64 []byte
)

func DeployAndStartDaemon(client *ssh.Client, user string) error {
	deployer := &DaemonDeployer{
		client: client,
		user:   user,
	}
	return deployer.Deploy()
}

type DaemonDeployer struct {
	client    *ssh.Client
	user      string
	daemonBin []byte
}

func (d *DaemonDeployer) Deploy() error {
	// Detect remote architecture and select the right binary
	arch, err := d.detectRemoteArch()
	if err != nil {
		return fmt.Errorf("failed to detect remote architecture: %w", err)
	}

	switch arch {
	case "x86_64", "amd64":
		d.daemonBin = daemonBinaryAmd64
	case "aarch64", "arm64":
		d.daemonBin = daemonBinaryArm64
	default:
		return fmt.Errorf("unsupported remote architecture: %s", arch)
	}

	if len(d.daemonBin) == 0 {
		return fmt.Errorf("daemon binary for %s not embedded - please build with 'make build'", arch)
	}

	if err := d.ensureDirectories(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	needsDeploy, err := d.needsDeployment()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if needsDeploy {
		fmt.Println("[pssh] Deploying daemon to remote server...")
		d.stopDaemon()
		if err := d.uploadDaemon(); err != nil {
			return fmt.Errorf("failed to upload daemon: %w", err)
		}
		fmt.Println("[pssh] Daemon uploaded successfully")
	}

	if err := d.ensureDaemonRunning(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	return nil
}

func (d *DaemonDeployer) detectRemoteArch() (string, error) {
	session, err := d.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	out, err := session.Output("uname -m")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

func (d *DaemonDeployer) ensureDirectories() error {
	remoteDir := fmt.Sprintf("/home/%s/.pssh", d.user)

	session, err := d.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	return session.Run(fmt.Sprintf("mkdir -p %s", remoteDir))
}

func (d *DaemonDeployer) needsDeployment() (bool, error) {
	daemonPath := fmt.Sprintf("/home/%s/.pssh/psshd", d.user)

	session, err := d.client.NewSession()
	if err != nil {
		return false, err
	}
	defer session.Close()

	// Check if the binary exists and compare its size to the embedded one.
	// A size mismatch means the daemon needs to be updated.
	out, err := session.Output(fmt.Sprintf("wc -c < %s 2>/dev/null", daemonPath))
	if err != nil {
		return true, nil
	}

	remoteSize := strings.TrimSpace(string(out))
	localSize := fmt.Sprintf("%d", len(d.daemonBin))
	if remoteSize != localSize {
		return true, nil
	}

	return false, nil
}

func (d *DaemonDeployer) stopDaemon() {
	daemonPath := fmt.Sprintf("/home/%s/.pssh/psshd", d.user)
	sockPath := fmt.Sprintf("/home/%s/.pssh/psshd.sock", d.user)

	session, err := d.client.NewSession()
	if err != nil {
		return
	}
	defer session.Close()

	killCmd := fmt.Sprintf("pkill -f '%s' 2>/dev/null; rm -f %s; sleep 0.5", daemonPath, sockPath)
	session.Run(killCmd)
}

func (d *DaemonDeployer) uploadDaemon() error {
	daemonPath := fmt.Sprintf("/home/%s/.pssh/psshd", d.user)

	session, err := d.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	go func() {
		stdin.Write(d.daemonBin)
		stdin.Close()
	}()

	return session.Run(fmt.Sprintf("cat > %s && chmod +x %s", daemonPath, daemonPath))
}

func (d *DaemonDeployer) ensureDaemonRunning() error {
	daemonPath := fmt.Sprintf("/home/%s/.pssh/psshd", d.user)
	sockPath := fmt.Sprintf("/home/%s/.pssh/psshd.sock", d.user)

	session, err := d.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	checkCmd := fmt.Sprintf("test -S %s && pgrep -f '%s' > /dev/null && echo 'running' || echo 'stopped'", sockPath, daemonPath)

	stdout, err := session.Output(checkCmd)
	if err != nil {
		return err
	}

	if strings.TrimSpace(string(stdout)) == "running" {
		return nil
	}

	fmt.Println("[pssh] Starting daemon...")

	// Remove stale socket file if it exists
	cleanSession, err := d.client.NewSession()
	if err != nil {
		return err
	}
	cleanSession.Run(fmt.Sprintf("rm -f %s", sockPath))
	cleanSession.Close()

	// Start the daemon in the background
	startSession, err := d.client.NewSession()
	if err != nil {
		return err
	}
	defer startSession.Close()

	startCmd := fmt.Sprintf("nohup %s > /dev/null 2>&1 & disown", daemonPath)
	err = startSession.Run(startCmd)
	if err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for the daemon socket to become available (up to 3 seconds)
	waitSession, err := d.client.NewSession()
	if err != nil {
		return err
	}
	defer waitSession.Close()

	waitCmd := fmt.Sprintf("for i in $(seq 1 30); do test -S %s && exit 0; sleep 0.1; done; exit 1", sockPath)
	err = waitSession.Run(waitCmd)
	if err != nil {
		return fmt.Errorf("daemon started but socket not ready at %s", sockPath)
	}

	return nil
}
