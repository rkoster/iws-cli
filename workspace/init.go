package workspace

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/ruben-koster/iws-cli/incus"
)

// Config contains the configuration for workspace initialization
type Config struct {
	InstanceName string
	Remote       string
}

// DestroyInstance removes an existing instance
func (w *Config) DestroyInstance(client *incus.Client, instanceName, remote string) error {
	// The client is already connected to the correct server, so we don't need the server prefix
	// Just use the instance name directly
	targetInstance := instanceName
	
	// Check if instance exists first
	running, err := client.IsInstanceRunning(targetInstance)
	if err != nil {
		// Instance doesn't exist, nothing to destroy
		fmt.Printf("Instance '%s' does not exist, nothing to destroy\n", instanceName)
		return nil
	}

	// Instance exists, stop it first if running
	if running {
		fmt.Printf("Stopping running instance '%s'...\n", instanceName)
		
		// Stop the instance with a reasonable timeout
		reqState := api.InstanceStatePut{
			Action:  "stop",
			Timeout: 30,
			Force:   false,
		}
		
		op, err := client.GetClient().UpdateInstanceState(targetInstance, reqState, "")
		if err != nil {
			return fmt.Errorf("failed to stop instance: %w", err)
		}
		
		if err := op.Wait(); err != nil {
			// If stop fails, try force stop
			fmt.Printf("Normal stop failed, trying force stop...\n")
			reqState.Force = true
			op, err = client.GetClient().UpdateInstanceState(targetInstance, reqState, "")
			if err != nil {
				return fmt.Errorf("failed to force stop instance: %w", err)
			}
			if err := op.Wait(); err != nil {
				return fmt.Errorf("failed to wait for instance force stop: %w", err)
			}
		}
		
		fmt.Printf("Instance '%s' stopped successfully\n", instanceName)
	}

	// Now destroy the instance
	fmt.Printf("Destroying existing instance '%s'...\n", instanceName)
	if err := client.DestroyInstance(targetInstance); err != nil {
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
	// Use su - ruben for a proper login shell with full environment setup
	ghosttyCmd := fmt.Sprintf("incus exec %s -- /bin/sh -c 'su - ruben -c \"exec tmux new-session -A -s main\"'", targetInstance)

	// Launch Ghostty with the incus exec command
	ghosttyPath := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	cmd := exec.Command(ghosttyPath, "--wait-after-command", fmt.Sprintf("--command=%s", ghosttyCmd))
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.Start()

	return nil
}

// ExecCommandInInstance executes a command in the instance
func ExecCommandInInstance(client *incus.Client, instanceName, remote string, command []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	targetInstance := instanceName
	if remote != "" {
		targetInstance = remote + ":" + instanceName
	}

	return client.ExecCommand(targetInstance, command, stdin, stdout, stderr)
}
