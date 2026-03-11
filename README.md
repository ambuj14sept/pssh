# pssh — Persistent SSH

[![License](https://img.shields.io/badge/license-WTFPL-blue.svg)](LICENSE)
[![Language](https://img.shields.io/badge/language-Bash-4EAA25.svg?logo=gnu-bash&logoColor=white)](https://www.gnu.org/software/bash/)
[![Nix](https://img.shields.io/badge/nix-supported-5277C3.svg?logo=nixos&logoColor=white)](https://nixos.org/)

Drop-in SSH replacement that gives you VS Code-like session persistence in your terminal.

**Your sessions survive connection drops, laptop sleep, and WiFi changes — automatically.**

```
Your Mac                          Linux Server
┌─────────────┐    SSH tunnel     ┌──────────────────────┐
│             │ ←──────────────→  │  tmux session (auto)  │
│  pssh       │    drops?         │  ├── ./binary-1       │
│  (reconnect │    reconnects &   │  └── keeps running    │
│   loop)     │    reattaches     │                       │
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

You never see or manage tmux. It's completely invisible.

## Install

```bash
# Clone
git clone https://github.com/ambuj14sept/pssh.git
cd pssh

# Install (symlinks to /usr/local/bin + adds shell aliases)
./install.sh
```

**Requirement:** `tmux` must be installed on the remote server(s):
```bash
# Ubuntu/Debian
sudo apt install tmux

# CentOS/RHEL
sudo yum install tmux
```

## Usage

### Basic — just like SSH

```bash
pssh user@server                    # persistent interactive shell
pssh user@server -- ./run-api       # run a binary persistently
pssh -p 2222 user@host              # custom SSH port
pssh -i ~/.ssh/mykey user@host      # custom SSH key
```

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
| WiFi drops | Auto-reconnects with exponential backoff (1s → 2s → 4s → ... → 30s cap) |
| Type `exit` | Session destroyed on server, pssh exits cleanly |
| Ctrl-C | Sent to the running binary (normal behavior), session stays alive |
| Mac reboots | Sessions still running on server. Use `pssh list` + `pssh attach` to reconnect |

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

~25 minutes of retrying before it gives up. Even then, your session is still alive on the server — reattach with `pssh attach`.

## Shell aliases

The installer adds these shortcuts to your shell:

| Alias | Command |
|-------|---------|
| `pl` | `pssh list` |
| `pa` | `pssh attach` |
| `pk` | `pssh kill` |
| `ps_status` | `pssh status` |

## How it works

1. When you run `pssh user@server`, it creates a tmux session on the server with an auto-generated unique name
2. SSH connects with aggressive keepalive settings to detect drops fast
3. If SSH exits with a non-zero code (connection dropped), pssh retries the connection and reattaches to the same tmux session
4. If SSH exits with code 0 (user typed `exit`), pssh checks if the tmux session still exists — if not, it exits cleanly
5. All of this is invisible to you — it feels like a normal SSH session that never breaks

## SSH options pass-through

All standard SSH options work:

```bash
pssh -p 2222 user@host                    # custom port
pssh -i ~/.ssh/mykey user@host            # identity file
pssh -J jump@bastion user@internal        # jump host / bastion
pssh -L 8080:localhost:80 user@host       # port forwarding
pssh -o "ProxyCommand=..." user@host      # any SSH option
```



## Nix Installation

### Quick run (no installation)

```bash
nix run github:ambuj14sept/pssh -- <args>
```

### Install via Flake

Add to your flake inputs:

```nix
{
  inputs.pssh.url = "github:ambuj14sept/pssh";

  outputs = { self, nixpkgs, pssh }: {
    # For home-manager or nixos configuration
    home.packages = [ pssh.packages.x86_64-linux.default ];
    # or
    environment.systemPackages = [ pssh.packages.x86_64-linux.default ];
  };
}
```

Or install directly:

```bash
nix profile install github:ambuj14sept/pssh
```
