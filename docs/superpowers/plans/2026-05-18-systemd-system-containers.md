# Systemd System Container Migration Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate iws-cli from creating app containers via Go API to creating system containers with native systemd via the `incus launch` CLI command.

**Architecture:** Replace `CreateInstanceFromImage` Go API calls with `incus launch` CLI (matching deskrun's approach). Add systemd readiness wait. Remove InitConfig (moved to container image). Simplify Ghostty exec to use `su - ruben`.

**Tech Stack:** Go 1.25.6, Incus v7 Go client (kept for status/stop/delete), `os/exec` for CLI calls

---

## Files Changed

| File | Change |
|------|--------|
| `incus/client.go` | Add `LaunchSystemContainer` (CLI-based), `WaitForSystemdReady`. Remove `LaunchInstance`. |
| `workspace/init.go` | Remove `InitConfig` and `AttachVolumes`. Simplify `LaunchGhostty` to use `su - ruben`. |
| `cmd/cmd.go` | Restructure into clear start/exec paths, remove InitConfig calls, deduplicate image pull logic. |

---

### Task 1: Add `LaunchSystemContainer` method to `incus/client.go`

**Files:**
- Modify: `incus/client.go:1` (add `"os/exec"` import — already present)

- [ ] **Step 1: Write the `LaunchSystemContainer` method**

Add a new method to `incus/client.go` (after `LaunchInstance`, around line 408). This replaces the Go API-based `LaunchInstance` with a CLI-based approach that creates system containers (matching deskrun's approach).

```go
// LaunchSystemContainer creates and starts a system container using the incus CLI.
// Unlike CreateInstanceFromImage (Go API), this ensures the container boots with
// init/systemd instead of running as an app container.
func (c *Client) LaunchSystemContainer(imageRef, instanceName, pool string) error {
	fmt.Printf("Launching %s\n", instanceName)

	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}

	// Determine storage pool
	if pool == "" {
		var err error
		pool, err = c.DetectStoragePool()
		if err != nil {
			return err
		}
	}

	// Create storage volumes if they don't exist
	if err := c.CreateVolumeIfNotExists(pool, "workspace-config"); err != nil {
		return err
	}
	if err := c.CreateVolumeIfNotExists(pool, "workspace"); err != nil {
		return err
	}

	// Build incus launch command
	args := []string{
		"launch",
		imageRef,
		instanceName,
		"-c", "security.nesting=true",
		"-d", fmt.Sprintf("config,type=disk,pool=%s,source=workspace-config,path=/home/ruben/.config-volume", pool),
		"-d", fmt.Sprintf("workspace,type=disk,pool=%s,source=workspace,path=/home/ruben/workspace", pool),
		"-n", "incusbr0",
	}

	if pool != "local" {
		args = append(args, "-s", pool)
	}

	cmd := exec.Command("incus", args...)

	// Set up environment to use the Incus config
	if c.Config.ConfigDir != "" {
		cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to launch container: %w: %s", err, string(output))
	}

	return nil
}
```

- [ ] **Step 2: Verify the code compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add incus/client.go
git commit -m "feat: add LaunchSystemContainer CLI-based method"
```

---

### Task 2: Add `WaitForSystemdReady` method to `incus/client.go`

**Files:**
- Modify: `incus/client.go` (add after `LaunchSystemContainer`)

- [ ] **Step 1: Write the `WaitForSystemdReady` method**

```go
// WaitForSystemdReady waits for systemd to be fully booted in the container.
// Returns when systemctl is-system-running returns "running" or "degraded".
// Times out after maxAttempts * pollInterval duration.
func (c *Client) WaitForSystemdReady(instanceName string, maxAttempts int, pollInterval time.Duration) error {
	fmt.Println("Waiting for systemd to be ready...")

	for i := 0; i < maxAttempts; i++ {
		cmd := exec.Command("incus", "exec", instanceName, "--", "systemctl", "is-system-running")

		if c.Config.ConfigDir != "" {
			cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			// systemctl may exit with non-zero if still booting
			time.Sleep(pollInterval)
			continue
		}

		status := strings.TrimSpace(string(output))
		if status == "running" || status == "degraded" {
			fmt.Printf("Systemd is %s\n", status)
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timed out waiting for systemd to be ready")
}
```

- [ ] **Step 2: Add `time` import if not present**

Add `"time"` to the imports in `incus/client.go` (line 3-10 area). The full import block should be:

```go
import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cliconfig"
)
```

- [ ] **Step 3: Verify the code compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add incus/client.go
git commit -m "feat: add WaitForSystemdReady polling method"
```

---

### Task 3: Remove `LaunchInstance` from `incus/client.go`

**Files:**
- Modify: `incus/client.go`

- [ ] **Step 1: Remove the `LaunchInstance` method**

Delete the entire `LaunchInstance` method (lines 288-408 in current file). This method uses the Go API's `CreateInstanceFromImage` which creates app containers.

After removal, the method immediately before it (`connectToServer`) should be followed by `LaunchSystemContainer` (added in Task 1), then `parseImageRef` (line 410).

- [ ] **Step 2: Also remove `parseImageRef` if unused**

Check that `parseImageRef` is not used anywhere else (it was only used by `LaunchInstance`). Remove the entire method.

- [ ] **Step 3: Verify the code compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add incus/client.go
git commit -m "refactor: remove LaunchInstance (Go API) in favor of CLI-based LaunchSystemContainer"
```

---

### Task 4: Simplify `LaunchGhostty` in `workspace/init.go`

**Files:**
- Modify: `workspace/init.go`

- [ ] **Step 1: Rewrite `LaunchGhostty` to use `su - ruben`**

Replace the existing `LaunchGhostty` method (lines 193-217) with a simplified version that uses `su - ruben` for a proper login shell:

```go
// LaunchGhostty opens the instance in a new Ghostty window
func (w *Config) LaunchGhostty(instance, remote string) error {
	targetInstance := instance
	if remote != "" {
		targetInstance = remote + instance
	}

	// Build the full command string for Ghostty's --command flag
	// Use su - ruben for a proper login shell with full environment setup
	ghosttyCmd := fmt.Sprintf("incus exec %s -- /bin/sh -c 'su - ruben -c \"exec tmux new-session -A -s main\"'", targetInstance)

	// Launch Ghostty with the incus exec command
	ghosttyPath := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	cmd := exec.Command(ghosttyPath, "--wait-after-command", fmt.Sprintf("--command=%s", ghosttyCmd))
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.Start()

	return nil
}
```

- [ ] **Step 2: Remove unused imports from `workspace/init.go`**

The `bytes` import is no longer needed (InitConfig used it). Remove it. The imports should be:

```go
import (
	"fmt"
	"os/exec"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/ruben-koster/iws-cli/incus"
)
```

- [ ] **Step 3: Verify the code compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add workspace/init.go
git commit -m "refactor: simplify LaunchGhostty to use su - ruben login shell"
```

---

### Task 5: Remove `InitConfig` and `AttachVolumes` from `workspace/init.go`

**Files:**
- Modify: `workspace/init.go`

- [ ] **Step 1: Remove `InitConfig` method**

Delete the entire `InitConfig` method (lines 20-115). This logic moves to the container image as a systemd oneshot service (see rkoster/nixpkgs#19).

- [ ] **Step 2: Remove `AttachVolumes` method**

Delete the entire `AttachVolumes` method (lines 117-135). Volumes are now attached during `incus launch` via `-d` flags.

- [ ] **Step 3: Remove unused imports**

After removing `InitConfig` and `AttachVolumes`, check what's still used:
- `api` — still used by `DestroyInstance`
- `fmt` — still used
- `os/exec` — still used by `LaunchGhostty`
- `github.com/ruben-koster/iws-cli/incus` — still used by `ExecCommandInInstance`
- `time` — no longer needed (was only in `InitConfig`)
- `io` — still needed by `ExecCommandInInstance`

Remove the `time` import.

- [ ] **Step 4: Verify the code compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add workspace/init.go
git commit -m "refactor: remove InitConfig and AttachVolumes (moved to container image)"
```

---

### Task 6: Restructure `cmd/cmd.go` — extract image pull helper and restructure flow

**Files:**
- Modify: `cmd/cmd.go`

- [ ] **Step 1: Extract image resolution helper**

Add a helper function to deduplicate the image pull logic (currently duplicated in the `--update` and normal paths):

```go
// resolveImage pulls the image to the target server and returns the launch reference.
// Returns the original image reference if pull fails (with a warning).
func resolveImage(client *incus.Client, image string, serverRemote, serverPrefix string) string {
	alias := "rkoster-workspace-latest"

	// For OCI images, always try to pull (PullImage handles OCI detection)
	pulledFingerprint, err := client.PullImage(image, alias)
	if err != nil {
		fmt.Printf("Warning: failed to pull image: %v\n", err)
		return image
	}

	if serverRemote != "" {
		return serverPrefix + alias
	}
	return "local:" + alias
}
```

- [ ] **Step 2: Rewrite `Execute` with clear start/exec paths**

Replace the entire `Execute` function with a cleaner structure:

```go
// Execute is the main entry point for the iws CLI
func Execute() error {
	cfg := config.New()

	// Parse command-line arguments
	if len(os.Args) > 1 {
		if err := cfg.ParseArguments(os.Args[1:]); err != nil {
			return fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	// Handle help flag
	if cfg.Help {
		printHelp()
		return nil
	}

	// Initialize Incus client (handles remote detection internally)
	client, err := incus.New()
	if err != nil {
		return fmt.Errorf("failed to initialize Incus client: %w", err)
	}

	// Get the detected server remote
	serverRemote := client.GetServerRemote()
	if serverRemote != "" {
		cfg.ServerRemote = serverRemote
		cfg.ServerPrefix = serverRemote + ":"
	} else {
		// On non-Linux platforms, a remote Incus server is required
		if runtime.GOOS != "linux" {
			return fmt.Errorf("no remote Incus server detected. On macOS, a remote server is required to run containers.")
		}
	}

	// Configure GHCR remote if needed
	if err := client.ConfigureGHCRRemote(); err != nil {
		return fmt.Errorf("failed to configure GHCR remote: %w", err)
	}

	// --- Start path: ensure container is running ---
	if cfg.Update {
		fmt.Println("Update requested: refreshing workspace image and recreating instance")
		wsConfig := &workspace.Config{
			InstanceName: cfg.InstanceName,
			Remote:       cfg.ServerPrefix,
		}
		if err := wsConfig.DestroyInstance(client, cfg.InstanceName, cfg.ServerPrefix); err != nil {
			return fmt.Errorf("failed to destroy instance: %w", err)
		}
	}

	running, err := client.IsInstanceRunning(cfg.InstanceName)
	if err != nil {
		// Instance doesn't exist, create it
		fmt.Printf("Launching %s\n", cfg.InstanceName)

		launchImage := resolveImage(client, cfg.Image, cfg.ServerRemote, cfg.ServerPrefix)

		pool := "local"
		if err := client.LaunchSystemContainer(launchImage, cfg.InstanceName, pool); err != nil {
			return fmt.Errorf("failed to launch container: %w", err)
		}

		if err := client.WaitForSystemdReady(cfg.InstanceName, 60, 2*time.Second); err != nil {
			return fmt.Errorf("failed waiting for systemd: %w", err)
		}
	} else if !running {
		// Instance exists but not running, start it
		fmt.Printf("Starting existing instance %s\n", cfg.InstanceName)
		if err := client.StartInstance(cfg.InstanceName); err != nil {
			return fmt.Errorf("failed to start instance: %w", err)
		}
		if err := client.WaitForSystemdReady(cfg.InstanceName, 60, 2*time.Second); err != nil {
			return fmt.Errorf("failed waiting for systemd: %w", err)
		}
	}

	// --- Exec path: launch user session ---
	wsConfig := &workspace.Config{
		InstanceName: cfg.InstanceName,
		Remote:       cfg.ServerPrefix,
	}
	if err := wsConfig.LaunchGhostty(cfg.InstanceName, cfg.ServerPrefix); err != nil {
		return fmt.Errorf("failed to launch Ghostty: %w", err)
	}

	return nil
}
```

- [ ] **Step 3: Add `time` import**

Add `"time"` to the imports in `cmd/cmd.go`:

```go
import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/ruben-koster/iws-cli/config"
	"github.com/ruben-koster/iws-cli/incus"
	"github.com/ruben-koster/iws-cli/workspace"
)
```

- [ ] **Step 4: Verify the code compiles**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add cmd/cmd.go
git commit -m "refactor: restructure Execute into clear start/exec paths with systemd wait"
```

---

### Task 7: Final verification and cleanup

**Files:**
- All modified files

- [ ] **Step 1: Full build verification**

Run: `go build ./...`
Expected: No errors

- [ ] **Step 2: Check for unused imports**

Run: `go vet ./...`
Expected: No errors/warnings

- [ ] **Step 3: Check git status is clean**

Run: `git status`
Expected: All changes staged, no untracked files

- [ ] **Step 4: Review diff**

Run: `git diff --cached`
Expected: Changes match the plan — no unexpected modifications

- [ ] **Step 5: Final commit (if not already committed)**

```bash
git add -A
git commit -m "refactor: migrate to Incus system containers with native systemd support"
```

---

## Self-Review

**Spec coverage:**
- ✅ Switch to `incus launch` CLI — Task 1 (LaunchSystemContainer), Task 3 (remove LaunchInstance)
- ✅ Add systemd readiness wait — Task 2 (WaitForSystemdReady)
- ✅ Remove InitConfig — Task 5
- ✅ Simplify LaunchGhostty — Task 4
- ✅ Restructure flow — Task 6

**Placeholder scan:** All code blocks are complete with actual implementations. No placeholders found.

**Type consistency:** All method signatures are consistent across tasks. `LaunchSystemContainer` uses the same `pool` parameter pattern as `LaunchInstance`. `WaitForSystemdReady` uses standard Go time.Duration.

**Edge cases handled:**
- Image pull failure falls back to original image reference (with warning)
- `WaitForSystemdReady` accepts "degraded" status (acceptable for systemd)
- `INCUS_DIR` env var set for CLI commands when config dir is configured
- Remote prefix handling preserved throughout
