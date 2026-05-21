package workspace

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/ruben-koster/iws-cli/incus"
)

// Config contains the configuration for workspace initialization
type Config struct {
	InstanceName string
	Remote       string
}

// DestroyInstance removes an existing instance
func (w *Config) DestroyInstance(client *incus.Client, instanceName, remote string) error {
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

// LaunchGhostty opens the instance in a new Ghostty window
func (w *Config) LaunchGhostty(instance, remote string) error {
	targetInstance := instance
	if remote != "" {
		targetInstance = remote + instance
	}

	// Build the full command string for Ghostty's --command flag
	// Use bash -lc to get full NixOS PATH, then su - ruben for user environment
	// Set TERM=xterm-256color since the VM doesn't have xterm-ghostty terminfo
	ghosttyCmd := fmt.Sprintf("incus exec -t %s -- bash -lc 'export TERM=xterm-256color; su - ruben -c \"exec tmux new-session -A -s main\"'", targetInstance)

	// Launch Ghostty with the incus exec command
	// Use -t (--force-interactive) to allocate a PTY so the incus-agent
	// can relay terminal size (COLUMNS/LINES) through to tmux/ghostty.
	// Without a PTY, incus exec runs in non-interactive mode and the
	// terminal size defaults to 80x24, leaving unused space at the bottom.
	ghosttyPath := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	cmd := exec.Command(ghosttyPath, "--wait-after-command", fmt.Sprintf("--command=%s", ghosttyCmd))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Start()

	return nil
}
