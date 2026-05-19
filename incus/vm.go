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
