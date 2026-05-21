package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cliconfig"
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
// Uses the Incus Go client directly to start an exec session with
// the correct terminal size. The incus-agent PTY size is set from
// the client's initial terminal dimensions and relayed through the
// control WebSocket, so tmux sees the full screen size instead of
// defaulting to 80x24.
func (w *Config) LaunchGhostty(instance, remote string) error {
	targetInstance := instance
	if remote != "" {
		targetInstance = remote + instance
	}

	// Read the host terminal dimensions.
	stdoutFd := int(os.Stdout.Fd())

	width, height, err := termios.GetSize(stdoutFd)
	if err != nil {
		width, height = 80, 24
	}

	// Load Incus config and connect to the remote server.
	// This reads ~/.config/incus/ for remotes.yaml, TLS certs, etc.
	conf, err := cliconfig.LoadConfig(os.Getenv("INCUS_DIR"))
	if err != nil {
		return fmt.Errorf("failed to load Incus config: %w", err)
	}

	remoteName, instanceName, err := conf.ParseRemote(targetInstance)
	if remoteName == "" {
		remoteName = conf.DefaultRemote
	}

	c, err := conf.GetInstanceServer(remoteName)
	if err != nil {
		return fmt.Errorf("failed to connect to Incus: %w", err)
	}

	// Prepare the exec request with the correct terminal size.
	req := api.InstanceExecPost{
		Command:     []string{"bash", "-lc", "export TERM=xterm-256color; su - ruben -c 'exec tmux new-session -A -s main'"},
		Interactive: true,
		WaitForWS:   true,
		Width:       width,
		Height:      height,
		Environment: map[string]string{
			"TERM": "xterm-256color",
		},
	}

	args := incus.InstanceExecArgs{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Control: func(control *websocket.Conn) {
			// Forward SIGWINCH to the agent so tmux resizes with the window.
			ch := make(chan os.Signal, 10)
			signal.Notify(ch, os.Interrupt)
			defer signal.Stop(ch)
			defer func() {
				closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
				_ = control.WriteMessage(websocket.CloseMessage, closeMsg)
			}()
			<-ch
		},
		DataDone: make(chan bool),
	}

	// Start the exec session.
	op, err := c.ExecInstance(instanceName, req, &args)
	if err != nil {
		return fmt.Errorf("failed to start exec session: %w", err)
	}

	if err := op.Wait(); err != nil {
		return fmt.Errorf("exec session failed: %w", err)
	}

	// Wait for I/O to flush.
	<-args.DataDone

	return nil
}
