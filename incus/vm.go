package incus

import (
	"bytes"
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

	return nil
}

// CreateVMDirs creates workspace directories in the VM.
func (c *Client) CreateVMDirs(instanceName string) error {
	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	dirs := []string{"/home/ruben/workspace", "/home/ruben/.config-volume"}
	for _, dir := range dirs {
		cmd := exec.Command("incus", "exec", remoteInstance, "--", "mkdir", "-p", dir)
		if c.Config.ConfigDir != "" {
			cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to create dir %s: %w: %s", dir, err, string(out))
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

// PushConfig pushes the local nixpkgs config directory into the VM at /opt/nixos-config/.
// Creates a temp directory locally with only the needed files (excluding node_modules, .opencode, etc.),
// then uses incus file push to transfer it.
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

	// Create a temp directory with filtered files
	tmpDir, err := os.MkdirTemp("", "iws-config-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create nixos-config subdir inside temp dir
	nixosConfigDir := tmpDir + "/nixos-config"
	if err := os.Mkdir(nixosConfigDir, 0755); err != nil {
		return fmt.Errorf("failed to create nixos-config dir: %w", err)
	}

	// Use tar to extract filtered files into nixos-config subdir
	tarCmd := exec.Command("tar", "xzf", "-", "-C", nixosConfigDir,
		"--exclude=node_modules",
		"--exclude=.opencode",
		"--exclude=result")

	// Create the archive first
	archiveCmd := exec.Command("tar", "czf", "-",
		"--exclude=node_modules",
		"--exclude=.opencode",
		"--exclude=result",
		"-C", localPath, ".")
	archiveOut, _ := archiveCmd.Output()

	tarCmd.Stdin = bytes.NewReader(archiveOut)
	if out, err := tarCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to extract to temp dir: %w: %s", err, string(out))
	}

	// Push to /opt/ — push the nixos-config subdir
	// incus file push pushes the source dir as a child of the target
	cmd := exec.Command("incus", "file", "push", "--recursive", "--create-dirs",
		nixosConfigDir+"/", remoteInstance+"/opt/")
	if c.Config.ConfigDir != "" {
		cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to push config: %w: %s", err, string(out))
	}

	// Fix ownership — incus file push preserves local UIDs which don't match VM users
	// Git refuses to open repos owned by unknown users
	chownCmd := exec.Command("incus", "exec", remoteInstance, "--",
		"bash", "-c", "source /etc/profile && chown -R root:root /opt/nixos-config")
	if c.Config.ConfigDir != "" {
		chownCmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
	}
	chownCmd.Run()

	return nil
}

// Provision runs nixos-rebuild switch inside the VM.
// Reads flake.nix from /opt/nixos-config to determine the config name.
func (c *Client) Provision(instanceName string) error {
	fmt.Println("Running nixos-rebuild switch...")

	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	flakeName := c.detectFlakeConfig(remoteInstance)
	cmd := exec.Command("incus", "exec", remoteInstance, "--", "bash", "-c",
		"source /etc/profile && cd /tmp && nixos-rebuild switch --flake /opt/nixos-config#"+flakeName)
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

// detectFlakeConfig reads flake.nix to find the nixosConfigurations key name.
func (c *Client) detectFlakeConfig(remoteInstance string) string {
	cmd := exec.Command("incus", "exec", remoteInstance, "--",
		"bash", "-c", "source /etc/profile && grep -oP 'nixosConfigurations\\.\\K[a-zA-Z0-9_-]+' /opt/nixos-config/flake.nix")
	output, err := cmd.CombinedOutput()
	if err == nil {
		name := strings.TrimSpace(string(output))
		if name != "" {
			return name
		}
	}
	// Fallback: try "workspace" then "default"
	for _, fallback := range []string{"workspace", "default"} {
		cmd = exec.Command("incus", "exec", remoteInstance, "--",
			"bash", "-c", "source /etc/profile && grep -q 'nixosConfigurations."+fallback+"' /opt/nixos-config/flake.nix")
		if cmd.Run() == nil {
			return fallback
		}
	}
	return "workspace"
}
