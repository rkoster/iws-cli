# VM Workspace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace OCI container workspace with an Incus VM launched from `images:nixos/25.11`, provisioned by pushing local NixOS config and running `nixos-rebuild switch`.

**Architecture:** iws-cli launches a NixOS VM via `incus launch --vm`, attaches persistent volumes, pushes `~/.config/iws/nixpkgs/` into the VM, and runs `nixos-rebuild switch` to provision. User connects via Ghostty + `incus exec`.

**Tech Stack:** Go, Incus CLI (`incus launch --vm`), NixOS, systemd

---

## File Structure

| File | Responsibility |
|------|---------------|
| `config/config.go` | CLI config: instance name, update flag, resource limits (cpu/memory/disk) |
| `incus/client.go` | Incus client: connection, storage pool detection, volume management |
| `incus/vm.go` | **NEW** — VM lifecycle: launch, wait for boot, push config, provision |
| `workspace/init.go` | Workspace operations: destroy, Ghostty launch |
| `cmd/cmd.go` | CLI entry point: orchestrates the full flow |

---

### Task 1: Simplify config — remove OCI, add VM resources

**Files:**
- Modify: `config/config.go`

- [ ] **Step 1: Rewrite config.go**

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	InstanceName string
	Update       bool
	Destroy      bool
	Help         bool
	ServerRemote string
	ServerPrefix string
	// VM resources
	CPU    string
	Memory string
	Disk   string
	// Config path
	NixpkgsPath string
}

func New() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		InstanceName: getEnv("INST", "workspace"),
		CPU:          getEnv("IWS_CPU", "4"),
		Memory:       getEnv("IWS_MEMORY", "8GiB"),
		Disk:         getEnv("IWS_DISK", "50GiB"),
		NixpkgsPath:  getEnv("IWS_NIXPKGS", filepath.Join(home, ".config", "iws", "nixpkgs")),
	}
}

func (c *Config) ParseArguments(args []string) error {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--update":
			c.Update = true
		case "--destroy":
			c.Destroy = true
		case "--help", "-h":
			c.Help = true
		default:
			if strings.Contains(args[i], "=") {
				parts := strings.SplitN(args[i], "=", 2)
				switch parts[0] {
				case "inst":
					c.InstanceName = parts[1]
				case "remote":
					c.ServerRemote = parts[1]
				case "cpu":
					c.CPU = parts[1]
				case "memory":
					c.Memory = parts[1]
				case "disk":
					c.Disk = parts[1]
				}
			}
		}
	}
	return nil
}

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add config/config.go
git commit -m "refactor: remove OCI config, add VM resource settings"
```

---

### Task 2: Create incus/vm.go — VM launch and provisioning

**Files:**
- Create: `incus/vm.go`

- [ ] **Step 1: Create incus/vm.go**

```go
package incus

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// LaunchVM creates and starts a NixOS VM with the given resource limits.
// Uses images:nixos/25.11 from the community image server.
func (c *Client) LaunchVM(instanceName, pool, cpu, memory, disk string) error {
	fmt.Printf("Launching VM %s (cpu=%s, memory=%s, disk=%s)\n", instanceName, cpu, memory, disk)

	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

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

	// Launch VM from NixOS image
	launchArgs := []string{
		"launch", "images:nixos/25.11", remoteInstance,
		"--vm",
		"-c", "limits.cpu=" + cpu,
		"-c", "limits.memory=" + memory,
		"-c", "security.nesting=true",
		"-d", "root,size=" + disk,
		"-n", "incusbr0",
	}

	if pool != "local" {
		launchArgs = append(launchArgs, "-s", pool)
	}

	cmd := exec.Command("incus", launchArgs...)
	if c.Config.ConfigDir != "" {
		cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to launch VM: %w: %s", err, string(output))
	}

	// Add disk devices for persistent volumes
	devices := []struct {
		name, pool, source, path string
	}{
		{"config", pool, "workspace-config", "/home/ruben/.config-volume"},
		{"workspace", pool, "workspace", "/home/ruben/workspace"},
	}

	for _, dev := range devices {
		addCmd := exec.Command("incus", "config", "device", "add", remoteInstance,
			dev.name, "disk",
			"pool="+dev.pool,
			"source="+dev.source,
			"path="+dev.path,
		)
		if c.Config.ConfigDir != "" {
			addCmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
		}
		if out, err := addCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to add device %s: %w: %s", dev.name, err, string(out))
		}
	}

	return nil
}

// WaitForBoot waits for the VM to be fully booted (systemd running + network up).
func (c *Client) WaitForBoot(instanceName string, maxAttempts int, pollInterval time.Duration) error {
	fmt.Println("Waiting for VM to boot...")

	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	for i := 0; i < maxAttempts; i++ {
		cmd := exec.Command("incus", "exec", remoteInstance, "--", "systemctl", "is-system-running")
		if c.Config.ConfigDir != "" {
			cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
		}

		output, err := cmd.CombinedOutput()
		if err == nil {
			status := strings.TrimSpace(string(output))
			if status == "running" || status == "degraded" {
				fmt.Printf("VM booted (systemd: %s)\n", status)
				return nil
			}
		}

		if i%10 == 0 {
			fmt.Printf("Waiting for boot... (attempt %d/%d)\n", i+1, maxAttempts)
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timed out waiting for VM to boot")
}

// PushConfig pushes the local nixpkgs config directory into the VM at /etc/nixos/.
func (c *Client) PushConfig(instanceName, localPath string) error {
	fmt.Printf("Pushing config from %s into VM...\n", localPath)

	// Validate local path exists
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		return fmt.Errorf("nixpkgs config directory not found: %s", localPath)
	}

	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	cmd := exec.Command("incus", "file", "push", "--recursive", "--create-dirs",
		localPath+"/", remoteInstance+"/etc/nixos/")
	if c.Config.ConfigDir != "" {
		cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to push config: %w: %s", err, string(output))
	}

	return nil
}

// Provision runs nixos-rebuild switch inside the VM.
func (c *Client) Provision(instanceName string) error {
	fmt.Println("Running nixos-rebuild switch...")

	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	cmd := exec.Command("incus", "exec", remoteInstance, "--", "nixos-rebuild", "switch")
	if c.Config.ConfigDir != "" {
		cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nixos-rebuild switch failed: %w", err)
	}

	fmt.Println("Provisioning complete")
	return nil
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add incus/vm.go
git commit -m "feat: add VM launch, boot wait, config push, and provisioning"
```

---

### Task 3: Rewrite cmd.go — VM-based flow

**Files:**
- Modify: `cmd/cmd.go`

- [ ] **Step 1: Rewrite cmd.go**

```go
package cmd

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/ruben-koster/iws-cli/config"
	"github.com/ruben-koster/iws-cli/incus"
	"github.com/ruben-koster/iws-cli/workspace"
)

func Execute() error {
	cfg := config.New()

	if len(os.Args) > 1 {
		if err := cfg.ParseArguments(os.Args[1:]); err != nil {
			return fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	if cfg.Help {
		printHelp()
		return nil
	}

	// Initialize Incus client
	client, err := incus.New()
	if err != nil {
		return fmt.Errorf("failed to initialize Incus client: %w", err)
	}

	serverRemote := client.GetServerRemote()
	if serverRemote != "" {
		cfg.ServerRemote = serverRemote
		cfg.ServerPrefix = serverRemote + ":"
	} else if runtime.GOOS != "linux" {
		return fmt.Errorf("no remote Incus server detected. On macOS, a remote server is required.")
	}

	// Handle --destroy
	if cfg.Destroy {
		wsConfig := &workspace.Config{InstanceName: cfg.InstanceName, Remote: cfg.ServerPrefix}
		return wsConfig.DestroyInstance(client, cfg.InstanceName, cfg.ServerPrefix)
	}

	// Check if VM exists and is running
	running, err := client.IsInstanceRunning(cfg.InstanceName)
	if err != nil {
		// VM doesn't exist — create it
		pool, err := client.DetectStoragePool()
		if err != nil {
			return fmt.Errorf("failed to detect storage pool: %w", err)
		}

		if err := client.LaunchVM(cfg.InstanceName, pool, cfg.CPU, cfg.Memory, cfg.Disk); err != nil {
			return fmt.Errorf("failed to launch VM: %w", err)
		}

		if err := client.WaitForBoot(cfg.InstanceName, 90, 2*time.Second); err != nil {
			return fmt.Errorf("VM boot failed: %w", err)
		}

		// Provision if config exists
		if _, statErr := os.Stat(cfg.NixpkgsPath); statErr == nil {
			if err := client.PushConfig(cfg.InstanceName, cfg.NixpkgsPath); err != nil {
				return fmt.Errorf("failed to push config: %w", err)
			}
			if err := client.Provision(cfg.InstanceName); err != nil {
				return fmt.Errorf("provisioning failed: %w", err)
			}
		} else {
			fmt.Printf("Note: no config at %s, launching vanilla NixOS\n", cfg.NixpkgsPath)
		}
	} else if !running {
		// VM exists but stopped — start it
		fmt.Printf("Starting VM %s\n", cfg.InstanceName)
		if err := client.StartInstance(cfg.InstanceName); err != nil {
			return fmt.Errorf("failed to start VM: %w", err)
		}
		if err := client.WaitForBoot(cfg.InstanceName, 90, 2*time.Second); err != nil {
			return fmt.Errorf("VM boot failed: %w", err)
		}
	} else if cfg.Update {
		// VM running + --update: push config and re-provision
		if _, statErr := os.Stat(cfg.NixpkgsPath); statErr != nil {
			return fmt.Errorf("config directory not found: %s", cfg.NixpkgsPath)
		}
		if err := client.PushConfig(cfg.InstanceName, cfg.NixpkgsPath); err != nil {
			return fmt.Errorf("failed to push config: %w", err)
		}
		if err := client.Provision(cfg.InstanceName); err != nil {
			return fmt.Errorf("provisioning failed: %w", err)
		}
	}

	// Connect via Ghostty
	wsConfig := &workspace.Config{InstanceName: cfg.InstanceName, Remote: cfg.ServerPrefix}
	return wsConfig.LaunchGhostty(cfg.InstanceName, cfg.ServerPrefix)
}

func printHelp() {
	fmt.Fprintf(os.Stderr, `Usage: iws [OPTIONS]

Launch a NixOS VM workspace and connect via Ghostty.

Options:
  --update          Push config and run nixos-rebuild switch
  --destroy         Stop and delete the VM (volumes preserved)
  --help, -h        Show this help message

Configuration:
  inst=NAME         Instance name (default: workspace, env: INST)
  cpu=N             CPU count (default: 4, env: IWS_CPU)
  memory=SIZE       Memory limit (default: 8GiB, env: IWS_MEMORY)
  disk=SIZE         Root disk size (default: 50GiB, env: IWS_DISK)

Config directory: ~/.config/iws/nixpkgs/ (env: IWS_NIXPKGS)

Examples:
  iws                       # Launch or connect to workspace VM
  iws --update              # Re-provision with latest config
  iws --destroy             # Delete VM (keeps volumes)
  iws cpu=8 memory=16GiB    # Custom resources
`)
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add cmd/cmd.go
git commit -m "refactor: rewrite CLI flow for VM-based workspace"
```

---

### Task 4: Clean up dead OCI code from incus/client.go

**Files:**
- Modify: `incus/client.go`

- [ ] **Step 1: Remove OCI-related functions**

Remove the following from `incus/client.go`:
- `ConfigureGHCRRemote()` method
- `PullImage()` method
- `connectToServer()` method (only used by PullImage)
- `CreateImageAlias()` method
- `EnsureNativeImage()` function and `NativeImageAlias` constant
- `LaunchSystemContainer()` method
- `WaitForSystemdReady()` method
- `splitImageRef()` function
- `AttachVolume()` method (unused)

Keep:
- `New()`, `connectToRemoteServer()`, `GetServerRemote()`, `GetClient()`
- `StartInstance()`, `IsInstanceRunning()`, `DestroyInstance()`
- `DetectStoragePool()`, `CreateVolumeIfNotExists()`
- `ExecCommand()`, `ExecCommandWithOutput()`

Also remove unused imports (`bytes`, `io`, `strings` if no longer needed).

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add incus/client.go
git commit -m "refactor: remove OCI image management code"
```

---

### Task 5: End-to-end test

**Files:** (none modified)

- [ ] **Step 1: Clean up any existing test workspace**

```bash
incus delete IncusOS:workspace --force 2>/dev/null; true
```

- [ ] **Step 2: Create minimal nixpkgs config for testing**

```bash
mkdir -p ~/.config/iws/nixpkgs
cat > ~/.config/iws/nixpkgs/configuration.nix << 'EOF'
{ config, pkgs, ... }:
{
  imports = [ <nixos/modules/virtualisation/incus-virtual-machine.nix> ];

  users.users.ruben = {
    isNormalUser = true;
    extraGroups = [ "wheel" "docker" ];
    initialPassword = "changeme";
    shell = pkgs.zsh;
  };

  programs.zsh.enable = true;

  environment.systemPackages = with pkgs; [
    git
    neovim
    tmux
    curl
    htop
  ];

  virtualisation.docker.enable = true;

  security.sudo.wheelNeedsPassword = false;

  system.stateVersion = "25.11";
}
EOF
```

- [ ] **Step 3: Run iws to create and provision VM**

```bash
go run ./main.go
```

Expected: VM launches, boots, config is pushed, nixos-rebuild runs, Ghostty opens.

- [ ] **Step 4: Test --update**

```bash
go run ./main.go --update
```

Expected: config pushed, nixos-rebuild runs, Ghostty opens.

- [ ] **Step 5: Test reconnect (no flags)**

```bash
go run ./main.go
```

Expected: connects immediately (VM already running).

- [ ] **Step 6: Test --destroy**

```bash
go run ./main.go --destroy
```

Expected: VM deleted, volumes preserved.

- [ ] **Step 7: Commit any fixes from testing**

```bash
git add -A
git commit -m "fix: adjustments from end-to-end VM testing"
```
