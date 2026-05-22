# iws-cli

Go CLI tool for managing Incus workspace VMs. Uses the [Incus CLI](https://linuxcontainers.org/incus/) for all operations.

## Features

- Automatic remote Incus server detection
- NixOS VM provisioning with persistent storage volumes
- Config push and nixos-rebuild convergence
- Ghostty terminal launch with dynamic username detection and tmux session management
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
./iws-cli --update                     # Re-provision with latest config
./iws-cli --destroy                    # Delete VM (keeps volumes)
./iws-cli inst=myworkspace             # Use a custom instance name
./iws-cli cpu=8 memory=16GiB           # Custom resources
```

### Environment Variables

| Variable    | Default                        | Description            |
|-------------|--------------------------------|------------------------|
| `INST`      | `workspace`                    | Instance name          |
| `IWS_CPU`   | `4`                            | CPU count              |
| `IWS_MEMORY`| `8GiB`                         | Memory limit           |
| `IWS_DISK`  | `50GiB`                        | Root disk size         |
| `IWS_NIXPKGS`| `~/.config/iws/nixpkgs/`     | NixOS config directory |

## How It Works

1. **Server detection** — Automatically finds the configured Incus remote server.
2. **VM creation** — Creates a NixOS VM with the specified resources, attaching workspace and config storage volumes.
3. **Config push** — Pushes the NixOS flake config into the VM and runs `nixos-rebuild switch`.
4. **Ghostty launch** — Opens a new Ghostty window connected via `incus exec`, dynamically detecting the VM username and starting a tmux session.

## Architecture

```
main.go
├── cmd/cmd.go          # CLI entry point, argument parsing, orchestration
├── config/config.go    # Configuration struct and defaults
├── incus/client.go     # Incus CLI wrapper (start, destroy, detect storage)
├── incus/vm.go         # VM lifecycle (launch, boot wait, config push, provision)
└── workspace/init.go   # Instance lifecycle, Ghostty launch
```

## License

[Apache License 2.0](LICENSE)
