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

// getVMIP reads the VM's IP address by querying ip inside the VM.
// This avoids the ambiguity of incus list which returns all interfaces
// including Docker bridges and host interfaces.
func getVMIP(instanceName string) (string, error) {
	cmd := exec.Command("incus", "exec", instanceName, "--", "ip", "-4", "addr", "show", "dev", "enp5s0")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get VM IP: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "inet ") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "inet" && i+1 < len(parts) {
					// Strip /CIDR suffix
					ip := strings.SplitN(parts[i+1], "/", 2)[0]
					return ip, nil
				}
			}
		}
	}
	return "", fmt.Errorf("VM IP address not found on enp5s0")
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

	// Build the SSH command with -t to force TTY allocation
	sshCmd := fmt.Sprintf("ssh -t ruben@%s", ip)

	// Launch Ghostty in a new window.
	ghosttyPath := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	cmd := exec.Command(ghosttyPath, "--wait-after-command", "--command="+sshCmd)
	cmd.Start()

	return nil
}
