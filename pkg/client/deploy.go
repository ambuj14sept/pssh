package client

import (
	_ "embed"
	"fmt"

	"golang.org/x/crypto/ssh"
)

var (
	//go:embed psshd
	daemonBinary []byte
)

func DeployAndStartDaemon(client *ssh.Client, user string) error {
	deployer := &DaemonDeployer{
		client: client,
		user:   user,
	}
	return deployer.Deploy()
}

type DaemonDeployer struct {
	client *ssh.Client
	user   string
}

func (d *DaemonDeployer) Deploy() error {
	if len(daemonBinary) == 0 {
		return fmt.Errorf("daemon binary not embedded - please build with 'make build'")
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

func (d *DaemonDeployer) ensureDirectories() error {
	remoteDir := fmt.Sprintf("/home/%s/.pssh", d.user)

	session, err := d.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	err = session.Run(fmt.Sprintf("mkdir -p %s", remoteDir))
	if err != nil {
		return err
	}

	return nil
}

func (d *DaemonDeployer) needsDeployment() (bool, error) {
	daemonPath := fmt.Sprintf("/home/%s/.pssh/psshd", d.user)

	session, err := d.client.NewSession()
	if err != nil {
		return false, err
	}
	defer session.Close()

	err = session.Run(fmt.Sprintf("test -x %s", daemonPath))
	if err != nil {
		return true, nil
	}

	return false, nil
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
		stdin.Write(daemonBinary)
		stdin.Close()
	}()

	err = session.Run(fmt.Sprintf("cat > %s && chmod +x %s", daemonPath, daemonPath))
	if err != nil {
		return err
	}

	return nil
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

	if string(stdout) == "running\n" {
		return nil
	}

	fmt.Println("[pssh] Starting daemon...")

	startSession, err := d.client.NewSession()
	if err != nil {
		return err
	}
	defer startSession.Close()

	err = startSession.Run(fmt.Sprintf("nohup %s > /dev/null 2>&1 &", daemonPath))
	if err != nil {
		return err
	}

	return nil
}
