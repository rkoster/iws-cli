# VM Workspace Design

## Summary

Replace the OCI-image-based container approach with a VM-based workspace using the official NixOS Incus image (`images:nixos/25.11`). Configuration is applied at runtime by pushing a local NixOS config directory into the VM and running `nixos-rebuild switch`.

## Motivation

- **Hypervisor isolation:** VMs protect the host from kernel panics and provide stronger security boundaries than containers.
- **Full systemd/Docker support:** VMs run a real kernel, avoiding OCI app-container limitations.
- **No image build/publish pipeline:** The base image comes from the community image server. Custom config lives in a git repo applied at runtime.
- **Follows proven deskrun model:** `incus launch` + push config + `nixos-rebuild switch`.

## Architecture

### Instance Type

Incus VM (not container). Uses `incus launch images:nixos/25.11 --vm` with the incus-agent pre-installed (standard for NixOS VM images).

### Base Image

`images:nixos/25.11` from the official Incus image server. No custom images to build or distribute.

### Configuration Source

The directory `~/.config/iws/nixpkgs/` on the developer's machine contains the NixOS system configuration. This is a checkout of `rkoster/nixpkgs` (to be restructured as a NixOS config repo).

### CLI Flow

| Command | Behavior |
|---------|----------|
| `iws` | Connect to running VM via Ghostty + `incus exec`. If VM doesn't exist, create it (launch + provision + connect). |
| `iws --update` | Push `~/.config/iws/nixpkgs/` into VM, run `nixos-rebuild switch`, then connect. If VM doesn't exist, create from scratch. |
| `iws --destroy` | Stop and delete the VM (volumes preserved). |

### VM Defaults

| Resource | Default | Override |
|----------|---------|----------|
| CPU | 4 | `--cpu` flag or `IWS_CPU` env |
| RAM | 8GB | `--memory` flag or `IWS_MEMORY` env |
| Root disk | 50GB | `--disk` flag or `IWS_DISK` env |
| Network | incusbr0 | - |
| security.nesting | true | - |

### Persistent Storage Volumes

Volumes survive VM rebuilds (`--update` with `--destroy` + recreate):

| Volume | Mount Path | Purpose |
|--------|-----------|---------|
| `workspace` | `/home/ruben/workspace` | Project files |
| `workspace-config` | `/home/ruben/.config-volume` | User config (dotfiles, SSH keys, etc.) |

### Provisioning Flow (`--update`)

1. Validate `~/.config/iws/nixpkgs/` exists
2. `incus file push --recursive ~/.config/iws/nixpkgs/ <VM>:/etc/nixos/`
3. `incus exec <VM> -- nixos-rebuild switch`
4. Report success/failure

### User Session

- Launch Ghostty with `incus exec <remote>:<instance> -- su - ruben`
- Incus agent required in VM (comes pre-installed with NixOS VM images)
- Multiple Ghostty windows can connect to the same VM

## Lifecycle

```
[No VM] --iws--> Launch VM → Wait for boot → Provision → Connect
[VM stopped] --iws--> Start VM → Wait for boot → Connect  
[VM running] --iws--> Connect
[VM running] --iws --update--> Push config → nixos-rebuild switch → Connect
[Any state] --iws --destroy--> Stop + Delete VM (keep volumes)
```

## What Changes in iws-cli

- Remove: OCI image pulling (`PullImage`, `ConfigureGHCRRemote`, `EnsureNativeImage`)
- Remove: OCI-related config (image references, GHCR remote setup)
- Change: `LaunchSystemContainer` → `LaunchVM` (uses `--vm` flag, resource limits)
- Add: `PushConfig` — pushes local nixpkgs dir into VM
- Add: `Provision` — runs `nixos-rebuild switch` inside VM
- Add: Resource flags (cpu, memory, disk)
- Keep: `WaitForSystemdReady`, persistent volumes, Ghostty launch, `DestroyInstance`

## What Changes in rkoster/nixpkgs

See upstream issue. The repo transitions from an OCI image builder to a NixOS configuration repository containing:

- `configuration.nix` — system-level config (Docker, networking, users, services)
- Home-manager module — user packages and dotfiles
- The existing package definitions can remain as inputs

## Error Handling

- If `~/.config/iws/nixpkgs/` doesn't exist on first launch: warn and launch with vanilla NixOS (functional but unconfigured)
- If `nixos-rebuild switch` fails: report error, VM remains in previous state, user can still connect
- If VM launch fails: report error, clean up partial resources

## Testing

- `iws` with no VM → creates VM, boots, connects
- `iws` with running VM → connects immediately
- `iws --update` → provisions and connects
- `iws --destroy` then `iws` → fresh VM with volumes retained
