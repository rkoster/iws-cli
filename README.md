# iws-cli

Go CLI tool for managing Incus workspace containers. Replaces the original `iws` bash script with a native Go implementation using the official [Incus Go client](https://github.com/lxc/incus).

## Features

- Automatic remote Incus server detection
- OCI image pulling from GHCR via Incus CLI
- Instance creation with storage volume attachment using the Incus Go API
- Config volume symlink initialization (`.config`, `opencode`, `gh`, etc.)
- Ghostty terminal launch with tmux session management
- Works on macOS with remote Incus servers

## Prerequisites

- [Go 1.26+](https://go.dev/dl/)
- [Incus](https://linuxcontainers.org/incus/) with a configured remote server
- [Ghostty](https://ghostty.org/) (for terminal launch)
- macOS (remote server required) or Linux (local or remote server)

## Installation

```bash
go build -o iws-cli .
```

## Usage

```bash
./iws-cli                              # Launch workspace (or open existing)
./iws-cli --update                     # Rebuild workspace from latest image
./iws-cli image=oci-ghcr:user/img:tag  # Use a custom image
./iws-cli inst=myworkspace             # Use a custom instance name
./iws-cli remote=myremote              # Specify a remote server name
```

### Environment Variables

| Variable   | Default                        | Description              |
|------------|--------------------------------|--------------------------|
| `INST`     | `workspace`                    | Instance name            |
| `IMAGE`    | `oci-ghcr:rkoster/workspace:latest` | OCI image reference |

## How It Works

1. **Server detection** — Automatically finds the configured Incus remote server.
2. **Image pull** — Pulls the OCI image to the Incus server using `incus image copy`.
3. **Instance creation** — Creates the container with `CreateInstanceFromImage`, attaching config and workspace volumes during creation.
4. **Config init** — Sets up symlinked config directories and state files from the config volume.
5. **Ghostty launch** — Opens a new Ghostty window connected to the container via `incus exec`, starting a tmux session.

## Architecture

```
main.go
├── cmd/cmd.go          # CLI entry point, argument parsing, orchestration
├── config/config.go    # Configuration struct and defaults
├── incus/client.go     # Incus client using official Go library
└── workspace/init.go   # Instance lifecycle, config init, Ghostty launch
```

## License

[Apache License 2.0](LICENSE)
