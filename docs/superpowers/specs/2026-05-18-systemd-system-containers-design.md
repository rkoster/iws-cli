# Design: Migrate iws-cli to Incus System Containers with Native Systemd

**Date:** 2026-05-18
**Status:** Approved
**Related:** [rkoster/nixpkgs#18](https://github.com/rkoster/nixpkgs/pull/18) (container image multi-user/systemd), [rkoster/nixpkgs#19](https://github.com/rkoster/nixpkgs/issues/19) (workspace-init systemd service)

## Problem

The workspace container currently runs as an "App" container (no systemd). The container image has been updated (nixpkgs PR #18) to support systemd as PID 1 with proper service management (nix-daemon, docker). iws-cli needs to launch containers as proper system containers with native systemd support.

## Root Cause

iws-cli uses the Go API `CreateInstanceFromImage` to create containers from OCI images. This creates "App" containers that run the image's entrypoint. The `incus launch` CLI command creates system containers that boot with init/systemd.

## Solution

### 1. Switch container creation from Go API to `incus launch` CLI

Replace `CreateInstanceFromImage` with shelling out to `incus launch`, which always creates system containers from OCI images (using the image as rootfs and booting with init).

```
incus launch <image> <name> \
  -c security.nesting=true \
  -d config,type=disk,pool=local,source=workspace-config,path=/home/ruben/.config-volume \
  -d workspace,type=disk,pool=local,source=workspace,path=/home/ruben/workspace \
  -n incusbr0
```

### 2. Add systemd readiness wait

After starting a container, wait for systemd to be fully booted before proceeding:

```
incus exec <instance> -- systemctl is-system-running --wait
```

Poll until it returns `running` or `degraded` (degraded is acceptable — means some non-critical unit failed).

### 3. Remove InitConfig

The `workspace/init.go` `InitConfig` function performs symlink initialization that will be handled by a systemd oneshot service in the container image (rkoster/nixpkgs#19). Remove this from iws-cli.

Until nixpkgs#19 is implemented, the InitConfig logic can remain as a temporary fallback, gated behind a check for whether `workspace-init.service` exists.

### 4. Simplify LaunchGhostty exec command

Current approach sources profile scripts and manually constructs PATH. With systemd + login shell:

```
incus exec <instance> -- su - ruben -c 'exec tmux new-session -A -s main'
```

`su - ruben` provides a proper login shell with full PAM session setup and environment.

### 5. Restructure main flow

```
Execute():
  if --update:
    stop + delete existing instance
    
  if instance doesn't exist:
    ensure volumes exist (Go API - keep)
    incus launch (CLI - new)
    wait for systemd ready (new)
    
  elif instance stopped:
    start instance (Go API - keep)
    wait for systemd ready (new)
    
  # Instance is running at this point
  launch Ghostty (simplified exec)
```

### What stays on Go API

- `IsInstanceRunning` — checking state
- `StartInstance` — starting stopped instances  
- `DestroyInstance` — stopping and deleting
- `CreateVolumeIfNotExists` — volume management
- `DetectStoragePool` — pool detection
- `ConfigureGHCRRemote` — remote configuration

### What moves to CLI

- Container creation: `incus launch`
- Image pull: `incus image copy` (already uses CLI)
- Systemd wait: `incus exec ... systemctl is-system-running`
- Ghostty exec: `incus exec ... su - ruben -c 'tmux ...'`

## Files Changed

| File | Change |
|------|--------|
| `incus/client.go` | Remove `LaunchInstance`/`CreateInstanceFromImage`, add `LaunchSystemContainer` (CLI-based), add `WaitForSystemd` |
| `workspace/init.go` | Remove `InitConfig`, simplify to just `LaunchGhostty` |
| `cmd/cmd.go` | Restructure into clear start/exec paths, remove InitConfig calls |

## Testing

```bash
# After changes, verify:
iws-cli  # Should create system container
incus list  # Should show CONTAINER (not CONTAINER (App))
incus exec workspace -- systemctl status  # Should show systemd running
```
