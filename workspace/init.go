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

// LaunchGhostty opens the instance in a new Ghostty window via SSH.
// Reads the VM's IP from incus list and launches `ssh ruben@<ip>` inside
// a new Ghostty window. SSH handles terminal sizing natively.
func (w *Config) LaunchGhostty(instance, remote string) error {
	// Get the VM's IP address
	ip, err := getVMIP(instance)
	if err != nil {
		return fmt.Errorf("failed to get VM IP: %w", err)
	}

	// Build the SSH command
	sshCmd := fmt.Sprintf("ssh ruben@%s", ip)

	// Launch Ghostty in a new window.
	ghosttyPath := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	cmd := exec.Command(ghosttyPath, "--wait-after-command", "--command="+sshCmd)
	cmd.Start()

	return nil
}
