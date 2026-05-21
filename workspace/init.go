package workspace

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/unix"

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

// getTerminalSize returns the current terminal's rows and columns from the
// controlling TTY. Falls back to 24x80 if the controlling fd is not a TTY.
func getTerminalSize() (rows, cols int) {
	var ws unix.Winsize
	if fd, err := unix.Open("/dev/tty", unix.O_RDONLY, 0); err == nil {
		unix.Close(fd)
		if _, _, err := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); err == 0 {
			return int(ws.Row), int(ws.Col)
		}
	}
	return 24, 80
}

// LaunchGhostty opens the instance in a new Ghostty window
func (w *Config) LaunchGhostty(instance, remote string) error {
	targetInstance := instance
	if remote != "" {
		targetInstance = remote + instance
	}

	// Read the host terminal size so we can pass it to the remote shell.
	// The incus-agent PTY does not relay the host's window size, causing
	// tmux to default to 80x24 and leave unused space at the bottom.
	rows, cols := getTerminalSize()

	// Build the full command string for Ghostty's --command flag.
	// Use bash -lc to get full NixOS PATH, then su - ruben for user environment.
	// Set TERM=xterm-256color since the VM doesn't have xterm-ghostty terminfo.
	// Pass COLUMNS and LINES via --env so tmux sees the correct terminal size.
	ghosttyCmd := fmt.Sprintf("incus exec --env COLUMNS=%d --env LINES=%d -t %s -- bash -lc 'export TERM=xterm-256color; su - ruben -c \"exec tmux new-session -A -s main\"'", cols, rows, targetInstance)

	ghosttyPath := "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	cmd := exec.Command(ghosttyPath, "--wait-after-command", fmt.Sprintf("--command=%s", ghosttyCmd))
	cmd.Start()

	return nil
}
