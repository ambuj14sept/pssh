# pssh — Persistent SSH

Drop-in SSH replacement that gives you VS Code-like session persistence in your terminal.

**Your sessions survive connection drops, laptop sleep, and WiFi changes — automatically.**

```
Your Machine                      Linux Server
┌─────────────┐    SSH tunnel     ┌──────────────────────┐
│             │ ←──────────────→  │  psshd (Go daemon)   │
│  pssh       │    drops?         │  ├── Session 1       │
│  (Go)       │    reconnects &   │  ├── Session 2       │
│             │    reattaches     │  └── keeps running   │
└─────────────┘                   └──────────────────────┘
```

## The Problem

1. SSH into a server, run a binary
2. Close your MacBook / WiFi drops / connection breaks
3. Open MacBook — connection dead, binary killed, start over

## The Solution

`pssh` wraps SSH with transparent session persistence:

- **Connection drops?** Auto-reconnects and reattaches — you're back where you were
- **Type `exit`?** Session is destroyed cleanly
- **Multiple terminals?** Each gets its own independent persistent session
- **Multiple servers?** Works naturally — just use different hosts
- **No configuration** — no session names, no config files, nothing to manage
- **Zero dependencies** — auto-deploys its own daemon, no tmux/apt/yum needed

## Install

### Prerequisites

- Go 1.18+ (for building)
- SSH access to your servers
- No dependencies required on remote servers!

### Build from Source

```bash
# Clone
git clone https://github.com/ambuj14sept/pssh.git
cd pssh

# Build
make build

# Install system-wide
sudo make install

# Or use locally
./bin/pssh user@server
```

### Build Output

```
bin/
├── pssh     # Client binary (9MB, embeds daemon)
└── psshd    # Daemon binary (3MB, deployed to servers)
```

## Uninstall

```bash
sudo rm /usr/local/bin/pssh
rm -rf ~/.pssh/
```

**Note:** The daemon (`~/.pssh/psshd`) on remote servers is not automatically removed.
To clean up a specific server:
```bash
pssh kill user@server --all
ssh user@server 'rm -rf ~/.pssh/'
```

## Usage

### Basic — just like SSH

```bash
pssh user@server                    # persistent interactive shell
pssh user@server -- ./run-api       # run a binary persistently
pssh -p 2222 user@host              # custom SSH port
pssh -i ~/.ssh/mykey user@host      # custom SSH key
```

### First Connection — Auto-Deploy

On the first connection to a server, `pssh` automatically:
1. Uploads the embedded daemon binary via SCP
2. Starts the daemon (`psshd`) in the background
3. Connects you to a new persistent session

```bash
$ pssh user@server
[pssh] Deploying daemon to remote server...
[pssh] Daemon uploaded successfully
[pssh] Starting daemon...
[pssh] Session pssh_1710345678_12345 established. Type exit to end.
user@server:~$ 
```

Subsequent connections reuse the already-running daemon.

### Multiple sessions across servers

```bash
# Terminal tab 1
pssh deploy@prod-server -- ./run-api

# Terminal tab 2
pssh deploy@prod-server -- ./run-worker

# Terminal tab 3
pssh admin@staging-server

# Terminal tab 4
pssh root@db-server -- htop
```

Each terminal tab gets its own independent session. All persist through disconnects.

### Managing sessions

```bash
# List active sessions on a server
pssh list user@server

# Reattach to a session (e.g., after Mac reboot)
pssh attach user@server pssh_1710345678_12345

# Kill a specific session
pssh kill user@server pssh_1710345678_12345

# Kill all pssh sessions on a server
pssh kill user@server --all

# Show all sessions started from this machine
pssh status
```

## What happens in each scenario

| Scenario | What happens |
|----------|-------------|
| Close MacBook lid | Session keeps running on server. pssh auto-reconnects when you open it |
| WiFi drops | Auto-reconnects with exponential backoff (1s → 2s → 4s → ... → 30s cap). Press **Enter** to retry immediately, **q** to quit |
| Type `exit` | Session destroyed on server, pssh exits cleanly |
| Ctrl-C | Sent to the running binary (normal behavior), session stays alive |
| Mac reboots | Sessions still running on server. Use `pssh list` + `pssh attach` to reconnect |
| Server reboots | Daemon stops, sessions lost. Reconnect with `pssh` to restart daemon |

## Retry behavior

When connection drops, pssh retries with exponential backoff:

```
Attempt 1:  wait 1s  → retry
Attempt 2:  wait 2s  → retry
Attempt 3:  wait 4s  → retry
Attempt 4:  wait 8s  → retry
Attempt 5:  wait 16s → retry
Attempt 6:  wait 30s → retry   ← max delay cap
...
Attempt 50: wait 30s → gives up
```

During any retry wait, press **Enter** to retry immediately or **q** to stop retrying and quit.

~25 minutes of retrying before it gives up. Even then, your session is still alive on the server — reattach with `pssh attach`.

## How it works

1. **First connect**: `pssh` SSHs to server, uploads the `psshd` daemon binary to `~/.pssh/`, and starts it
2. **Daemon**: Runs on the server, manages PTY sessions via Unix socket at `~/.pssh/psshd.sock`
3. **Session**: Client creates/attaches to a session, proxies stdin/stdout through the daemon
4. **Reconnect**: If SSH drops, client detects it, re-SSHs, and reattaches to the same session
5. **Cleanup**: When you type `exit`, the session ends and daemon cleans up

**Key differences from original bash version:**
- ❌ No tmux dependency (replaced with custom Go daemon)
- ❌ No cron setup (daemon starts on-demand, like VS Code)
- ✅ Single binary (client embeds daemon)
- ✅ Better error handling and logging
- ✅ Cross-platform (Linux, macOS, BSD)

## SSH options pass-through

All standard SSH options work:

```bash
pssh -p 2222 user@host                    # custom port
pssh -i ~/.ssh/mykey user@host            # identity file
pssh -J jump@bastion user@internal        # jump host / bastion
pssh -L 8080:localhost:80 user@host       # port forwarding
pssh -o "ProxyCommand=..." user@host      # any SSH option
```

## Development

### Build

```bash
# Build both binaries
make build

# Build just daemon
make build-daemon

# Build just client (requires daemon already built)
make build-client
```

### Test

```bash
make test
```

### Format code

```bash
make fmt
```

### Clean build artifacts

```bash
make clean
```

## Project Structure

```
pssh/
├── cmd/
│   ├── pssh/          # Client CLI entry point
│   └── psshd/         # Daemon entry point
├── pkg/
│   ├── protocol/      # JSON message protocol
│   ├── daemon/        # Server-side session management
│   ├── client/        # SSH, deployment, session proxy
│   └── config/        # Configuration and paths
├── bin/               # Build output
├── Makefile
├── go.mod
└── README.md
```

## Architecture

```
Client (local machine)          Server (remote)
┌─────────────────┐             ┌─────────────────┐
│ pssh binary     │──SSH──→     │ ~/.pssh/psshd   │
│ (embeds psshd)  │             │ (Go daemon)     │
│                 │             │                 │
│ - SSH connect   │             │ - Unix socket   │
│ - Deploy/start  │             │ - PTY sessions  │
│ - Proxy I/O     │             │ - JSON protocol │
└─────────────────┘             └─────────────────┘
```

The client embeds the daemon binary using Go's `//go:embed` directive. On first connect, it SCPs the daemon to the server and starts it. The daemon persists sessions and survives SSH disconnections.

## Requirements

**Local machine:**
- SSH client
- Terminal with PTY support

**Remote server:**
- SSH server
- Nothing else! The daemon is auto-deployed.

**No root required** — daemon runs in user space.

## License

WTFPL - Do What The F*ck You Want To Public License
