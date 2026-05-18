package cmd

import (
	"bytes"
	"fmt"
	"os"
	"runtime"

	"github.com/ruben-koster/iws-cli/config"
	"github.com/ruben-koster/iws-cli/incus"
	"github.com/ruben-koster/iws-cli/workspace"
)

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

	// Handle update flag
	if cfg.Update {
		fmt.Println("Update requested: refreshing workspace image and recreating instance")

		// Destroy existing instance
		wsConfig := &workspace.Config{
			InstanceName: cfg.InstanceName,
			Remote:       cfg.ServerPrefix,
		}

		if err := wsConfig.DestroyInstance(client, cfg.InstanceName, cfg.ServerPrefix); err != nil {
			return fmt.Errorf("failed to destroy instance: %w", err)
		}

		// Pull latest image
		alias := "rkoster-workspace-latest"
		var launchImage string
		if cfg.ServerRemote != "" {
			if _, err := client.PullImage(cfg.Image, alias); err != nil {
				return fmt.Errorf("failed to pull image to server: %w", err)
			}
			launchImage = cfg.ServerPrefix + alias
		} else {
			if _, err := client.PullImage(cfg.Image, alias); err != nil {
				return fmt.Errorf("failed to pull image locally: %w", err)
			}
			launchImage = "local:" + alias
		}

		// Launch instance (volumes are attached during creation)
		pool := "local"
		if err := client.LaunchSystemContainer(launchImage, cfg.InstanceName, pool); err != nil {
			return fmt.Errorf("failed to launch instance: %w", err)
		}

		// Wait for container to be ready
		fmt.Println("Waiting for container to be ready...")

		// Initialize config
		if err := wsConfig.InitConfig(client, cfg.InstanceName, cfg.ServerPrefix); err != nil {
			return fmt.Errorf("failed to initialize config: %w", err)
		}
	} else {
		// Check if instance exists and is running
		running, err := client.IsInstanceRunning(cfg.InstanceName)
		if err != nil {
			// Instance doesn't exist, create it
			fmt.Printf("Launching %s\n", cfg.InstanceName)

			alias := "rkoster-workspace-latest"
			var launchImage string
			if cfg.ServerRemote != "" {
				if _, err := client.PullImage(cfg.Image, alias); err != nil {
					fmt.Printf("Warning: failed to pull image to server: %v\n", err)
					launchImage = cfg.Image
				} else {
					launchImage = cfg.ServerPrefix + alias
				}
			} else {
				if _, err := client.PullImage(cfg.Image, alias); err != nil {
					fmt.Printf("Warning: failed to pull image locally: %v\n", err)
					launchImage = cfg.Image
				} else {
					launchImage = "local:" + alias
				}
			}

			pool := "local"
			if err := client.LaunchSystemContainer(launchImage, cfg.InstanceName, pool); err != nil {
				return fmt.Errorf("failed to launch instance: %w", err)
			}

			// Wait for container to be ready
			fmt.Println("Waiting for container to be ready...")

			// Initialize config
			wsConfig := &workspace.Config{
				InstanceName: cfg.InstanceName,
				Remote:       cfg.ServerPrefix,
			}
			if err := wsConfig.InitConfig(client, cfg.InstanceName, cfg.ServerPrefix); err != nil {
				return fmt.Errorf("failed to initialize config: %w", err)
			}
		} else if !running {
			// Instance exists but not running, start it
			if err := client.StartInstance(cfg.InstanceName); err != nil {
				return fmt.Errorf("failed to start instance: %w", err)
			}
		}
	}

	// Launch Ghostty
	wsConfig := &workspace.Config{
		InstanceName: cfg.InstanceName,
		Remote:       cfg.ServerPrefix,
	}
	if err := wsConfig.LaunchGhostty(cfg.InstanceName, cfg.ServerPrefix); err != nil {
		return fmt.Errorf("failed to launch Ghostty: %w", err)
	}

	return nil
}

func printHelp() {
	fmt.Fprintf(os.Stderr, `Usage: iws-cli [OPTIONS] [IMAGE] [REMOTE]

Launch an Incus workspace container in Ghostty.

Options:
  --update          Rebuild workspace from latest image
  --help, -h        Show this help message

Arguments:
  IMAGE             OCI image reference (default: oci-ghcr:rkoster/workspace:latest)
  REMOTE            Incus remote server name

Environment Variables:
  INST              Instance name (default: workspace)
  IMAGE             OCI image reference

Examples:
  iws-cli                           # Launch default workspace
  iws-cli --update                  # Rebuild from latest image
  iws-cli image=user/img:tag        # Use custom image
  iws-cli inst=myworkspace          # Use custom instance name
  iws-cli remote=myremote           # Use specific remote
`)
}

// ExecCommandInInstance executes a command in the instance and returns the output
func ExecCommandInInstance(client *incus.Client, instanceName, remote string, command []string) (string, error) {
	var stdout bytes.Buffer
	err := client.ExecCommand(instanceName, command, nil, &stdout, nil)
	return stdout.String(), err
}
