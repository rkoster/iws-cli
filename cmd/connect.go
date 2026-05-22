package cmd

import (
	"fmt"

	"github.com/ruben-koster/iws-cli/incus"
)

// handleConnect handles the "iws connect" subcommand.
// It connects to the Incus server via the Go client and launches an
// interactive PTY session inside the VM, with proper terminal sizing
// and resize event relay via the control WebSocket.
func handleConnect(instance, remote string) error {
	// Connect via Incus Go client (runs inside Ghostty, reads terminal size from Ghostty's fd)
	return incus.ConnectAndExec(instance, remote)
}

// handleConnectWithClient is an alternative entry point that uses the CLI-based
// incus.Client to verify the VM is running before connecting.
func handleConnectWithClient(client *incus.Client, instance, remote string) error {
	// Verify instance is running
	running, err := client.IsInstanceRunning(instance)
	if err != nil {
		return fmt.Errorf("instance %s is not running: %w", instance, err)
	}
	if !running {
		return fmt.Errorf("instance %s is not running (start it with: iws)", instance)
	}

	// Connect via Go client with proper terminal sizing
	return incus.ConnectAndExec(instance, remote)
}
