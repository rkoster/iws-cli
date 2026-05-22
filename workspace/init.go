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
// Uses `open -na Ghostty.app --args --command=iws connect` which runs
// the Incus Go client inside Ghostty. The Go client reads the terminal
// size from Ghostty's fd and relays SIGWINCH via the control WebSocket.
func (w *Config) LaunchGhostty(instance, remote string) error {
	targetInstance := instance
	if remote != "" {
		targetInstance = remote + instance
	}

	// Build the command: "iws connect workspace --remote IncusOS"
	// The 'iws' binary runs inside Ghostty, reads Ghostty's terminal size,
	// and connects via Incus Go client with control WebSocket resize relay.
	ghosttyCmd := fmt.Sprintf("iws connect %s --remote '%s'", targetInstance, remote)

	// Launch Ghostty in a new window.
	ghosttyPath := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	cmd := exec.Command(ghosttyPath, "--wait-after-command", "--command="+ghosttyCmd)
	cmd.Start()

	return nil
}
