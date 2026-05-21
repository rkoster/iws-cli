package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lxc/incus/v7/shared/termios"

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

// getVMIP reads the VM's IP address from incus list.
func getVMIP(instanceName string) (string, error) {
	cmd := exec.Command("incus", "list", instanceName, "-c", "4", "--format", "csv")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get VM IP: %w", err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("VM IP address not found")
	}
	return ip, nil
}

// LaunchGhostty opens the instance in a new Ghostty window.
// Uses `open -na Ghostty.app --args --command=...` to spawn a new
// Ghostty window running `incus exec -t` with the correct terminal
// size set via COLUMNS/LINES env vars.
func (w *Config) LaunchGhostty(instance, remote string) error {
	targetInstance := instance
	if remote != "" {
		targetInstance = remote + instance
	}

	// Read the host terminal dimensions so we can pass them to the
	// remote shell. The incus-agent PTY does not relay the host's
	// window size, so we read it here and pass COLUMNS/LINES via env.
	stdoutFd := int(os.Stdout.Fd())
	width, height, err := termios.GetSize(stdoutFd)
	if err != nil {
		width, height = 80, 24
	}

	// Build the command string for Ghostty.
	// Use incus exec -t to get a PTY, pass COLUMNS/LINES so tmux
	// sees the correct terminal size, and start a tmux session.
	ghosttyCmd := fmt.Sprintf("incus exec -t %s -- bash -lc 'export TERM=xterm-256color; COLUMNS=%d LINES=%d su - ruben -c \"exec tmux new-session -A -s main\"'", targetInstance, width, height)

	// Launch Ghostty in a new window.
	ghosttyPath := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	cmd := exec.Command(ghosttyPath, "--wait-after-command", "--command="+ghosttyCmd)
	cmd.Start()

	return nil
}
