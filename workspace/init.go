package workspace

import (
	"fmt"
	"os/exec"
	"strings"

	iwsincus "github.com/ruben-koster/iws-cli/incus"
)

// Config contains the configuration for workspace initialization
type Config struct {
	InstanceName string
	Remote       string
}

// DestroyInstance removes an existing instance
func (w *Config) DestroyInstance(client *iwsincus.Client, instanceName, remote string) error {
	// Check if instance exists first
	_, err := client.IsInstanceRunning(instanceName)
	if err != nil {
		fmt.Printf("Instance '%s' does not exist, nothing to destroy\n", instanceName)
		return nil
	}

	// Destroy with force (handles running instances)
	fmt.Printf("Destroying existing instance '%s'...\n", instanceName)
	if err := client.DestroyInstance(instanceName); err != nil {
		return fmt.Errorf("failed to destroy instance: %w", err)
	}

	fmt.Printf("Instance '%s' destroyed successfully\n", instanceName)
	return nil
}

// getUsername detects the first user home directory on the VM by running
// `incus exec <remote>:<instance> -- ls /home` and returning the first
// directory name found.
func getUsername(instance, remote string) (string, error) {
	// Build the instance reference (e.g. "remote:workspace" or "workspace")
	instanceRef := instance
	if remote != "" {
		instanceRef = remote + instance
	}

	cmd := exec.Command("incus", "exec", instanceRef, "--", "ls", "/home")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to list /home on VM: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			return line, nil
		}
	}

	return "", fmt.Errorf("no user home directory found on VM")
}

// LaunchGhostty opens the instance in a new Ghostty window via incus exec.
// It detects the VM username dynamically, then launches Ghostty with
// `open -na Ghostty.app --args --command='incus exec -t ...'` to start
// a tmux session.
func (w *Config) LaunchGhostty(instance, remote string) error {
	user, err := getUsername(instance, remote)
	if err != nil {
		return fmt.Errorf("failed to detect username: %w", err)
	}

	// Build the instance reference (e.g. "remote:workspace" or "workspace")
	instanceRef := instance
	if remote != "" {
		instanceRef = remote + instance
	}

	// Build the incus exec command string
	incusCmd := fmt.Sprintf("incus exec -t --env TERM=xterm-256color %s -- su %s -c \"sleep 1 && tmux new-session -A -s main\"",
		instanceRef, user)

	// Launch Ghostty via `open`
	cmd := exec.Command("open", "-na", "Ghostty.app", "--args", "--command="+incusCmd)
	cmd.Start()

	return nil
}
