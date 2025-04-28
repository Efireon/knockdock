# Knockdock - CloudFlare SSH Tunnel

A simple utility to expose your SSH server to the internet securely via Cloudflare. Access your SSH server from anywhere without complex VPN setup or static IP.

## Features

- **Zero Configuration**: Auto-setup with one command
- **Background Operation**: Runs as daemon process
- **Cross-platform**: Works on most Linux distributions
- **Simple CLI**: Easy commands for all operations

## Installation

```bash
# Build from source
git clone https://github.com/Efireon/knockdock.git
cd knockdock
go build -o knockdock main.go

# Option 1: Make executable in current directory
1. sudo chmod +x knockdock

# OR

# Option 2: Install system-wide
2. sudo mv knockdock /usr/local/bin/ && sudo chmod +x /usr/local/bin/knockdock
```

## Usage

### Basic commands

```bash
# Start tunnel
knockdock start

# Check status
knockdock status

# Stop tunnel
knockdock stop

# Remove all changes
knockdock purge
```

### Example output

```
======================================================================
SSH Tunnel Setup Complete!

Tunnel URL: https://example-name.trycloudflare.com
Daemon PID: 12345

Connection Options:

1. Using ProxyCommand:
ssh -o ProxyCommand="cloudflared access tcp --hostname example-name.trycloudflare.com" $user$@example-name.trycloudflare.com

2. Using Local Port(On your local machine):
cloudflared access tcp --hostname example-name.trycloudflare.com --url localhost:2222
ssh $user$@localhost -p 2222
======================================================================
```

### Command-line options

```
Usage: knockdock [options] command

Commands:
  start   - Start SSH tunnel
  stop    - Stop running SSH tunnel
  status  - Display current tunnel status
  purge   - Clean up all changes

Options:
  -h        Show help
  -port     Port for metrics (default: 8080)
  -timeout  Timeout in seconds (default: 120)
  -v        Enable verbose output
```

## Troubleshooting

- **Tunnel creation timeout**: Check internet connection and firewall
- **Connection issues**: Verify SSH server is running with `systemctl status sshd`
- **Log file location**: `~/.kntunnel/tunnel.log`

## Requirements

- Linux OS (Ubuntu, Debian, Arch, Astra Linux, etc.)
- SSH server installed
- `curl`, `bash`, and basic system tools
