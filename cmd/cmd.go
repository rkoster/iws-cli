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

		pool, err := client.DetectStoragePool()
		if err != nil {
			return fmt.Errorf("failed to detect storage pool: %w", err)
		}
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

// resolveImage pulls the image to the target server and returns the launch reference.
// Returns the original image reference if pull fails (with a warning).
func resolveImage(client *incus.Client, image string, serverRemote, serverPrefix string) string {
	alias := "rkoster-workspace-latest"

	// For OCI images, always try to pull (PullImage handles OCI detection)
	_, err := client.PullImage(image, alias)
	if err != nil {
		fmt.Printf("Warning: failed to pull image: %v\n", err)
		return image
	}

	if serverRemote != "" {
		return serverPrefix + alias
	}
	return "local:" + alias
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


