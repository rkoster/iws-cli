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

		// Create workspace directories in the VM
		if err := client.CreateVMDirs(cfg.InstanceName); err != nil {
			return fmt.Errorf("failed to create workspace dirs: %w", err)
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
		// VM running + --update: converge VM config and re-provision
		pool, _ := client.DetectStoragePool()
		if pool != "" {
			if err := client.EnsureVolumes(cfg.InstanceName, pool); err != nil {
				return fmt.Errorf("failed to ensure volumes: %w", err)
			}
		}
		if err := client.CreateVMDirs(cfg.InstanceName); err != nil {
			return fmt.Errorf("failed to fix directory ownership: %w", err)
		}
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
