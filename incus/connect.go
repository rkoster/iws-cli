package incus

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cliconfig"
	"github.com/lxc/incus/v7/shared/termios"
	"golang.org/x/sys/unix"
)

// ConnectAndExec connects to the Incus server and executes an interactive
// command inside the specified instance, relaying terminal I/O and resize events.
// The terminal size is read from the current process's stdout fd (which is
// Ghostty's terminal when run inside a Ghostty window).
func ConnectAndExec(instance, remote string) error {
	// Load Incus config to get TLS certs from ~/.config/incus/
	cfg, err := cliconfig.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load incus config: %w", err)
	}

	// Determine the remote name
	var remoteName string
	// Strip trailing colon if present (e.g. "IncusOS:" -> "IncusOS")
	cleanRemote := strings.TrimSuffix(remote, ":")
	if cleanRemote != "" {
		// Check if remote looks like a remote name (exists in config)
		if _, ok := cfg.Remotes[cleanRemote]; ok {
			remoteName = cleanRemote
		} else {
			// Treat as a server address — resolve to the matching remote
			for name, r := range cfg.Remotes {
				if r.Protocol == "incus" && r.Static {
					if r.Addr == cleanRemote {
						remoteName = name
						break
					}
				}
			}
		}
	}

	if remoteName == "" {
		// Fall back to finding any Incus remote (not just static)
		for name, r := range cfg.Remotes {
			if r.Protocol == "incus" && name != "local" {
				remoteName = name
				break
			}
		}
	}

	if remoteName == "" {
		return fmt.Errorf("no Incus remote configured")
	}

	// Get a connected server instance using the config (handles TLS certs, keepalive, etc.)
	server, err := cfg.GetInstanceServer(remoteName)
	if err != nil {
		return fmt.Errorf("failed to connect to Incus: %w", err)
	}

	// Read terminal size from Ghostty's terminal fd
	stdoutFd := int(os.Stdout.Fd())
	width, height, err := termios.GetSize(stdoutFd)
	if err != nil {
		width, height = 80, 24
	}

	// Prepare the exec request
	req := api.InstanceExecPost{
		Command:     []string{"bash", "-lc", "export TERM=xterm-256color; exec su - ruben -c 'exec tmux new-session -A -s main'"},
		WaitForWS:   true,
		Interactive: true,
		Environment: map[string]string{"TERM": "xterm-256color"},
		Width:       width,
		Height:      height,
	}

	// Create a stop channel for the control handler goroutine
	stopCh := make(chan struct{})

	// Prepare exec args with stdin/stdout/stderr and control handler
	execArgs := &incus.InstanceExecArgs{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Control:  func(conn *websocket.Conn) { controlSocketHandler(conn, stopCh) },
		DataDone: make(chan bool),
	}

	// Execute the command
	fullInstance := remoteName + ":" + instance
	if remoteName == "local" {
		fullInstance = instance
	}

	fmt.Printf("Connecting to %s (terminal: %dx%d)...\n", fullInstance, width, height)

	op, err := server.ExecInstance(instance, req, execArgs)
	if err != nil {
		return fmt.Errorf("failed to exec instance: %w", err)
	}

	// Wait for the operation to complete
	if err := op.Wait(); err != nil {
		return fmt.Errorf("exec failed: %w", err)
	}

	// Wait for I/O to flush
	<-execArgs.DataDone

	// Signal the control handler to stop
	close(stopCh)

	return nil
}

// controlSocketHandler handles the control WebSocket connection for terminal
// resize (SIGWINCH) and signal forwarding. It mirrors the behavior of the
// incus CLI's exec command. The stop channel is closed when the exec session
// ends, allowing this goroutine to exit.
func controlSocketHandler(control *websocket.Conn, stop <-chan struct{}) {
	if runtime.GOOS == "windows" {
		// Windows doesn't support SIGWINCH via unix signals
		// Consume pings until the session ends or connection closes
		for {
			_, _, err := control.ReadMessage()
			if err != nil {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}

	ch := make(chan os.Signal, 10)
	signal.Notify(ch,
		unix.SIGWINCH,
		unix.SIGTERM,
		unix.SIGHUP,
		unix.SIGINT,
		unix.SIGQUIT,
		unix.SIGABRT,
		unix.SIGTSTP,
		unix.SIGTTIN,
		unix.SIGTTOU,
		unix.SIGUSR1,
		unix.SIGUSR2,
		unix.SIGSEGV,
		unix.SIGCONT)

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	defer func() { _ = control.WriteMessage(websocket.CloseMessage, closeMsg) }()

	for {
		select {
		case sig := <-ch:
			switch sig {
			case unix.SIGWINCH:
				// Send window-resize to the incus-agent
				sendTermSize(control)

			case unix.SIGTERM, unix.SIGHUP, unix.SIGINT, unix.SIGQUIT,
				unix.SIGABRT, unix.SIGTSTP, unix.SIGTTIN, unix.SIGTTOU,
				unix.SIGUSR1, unix.SIGUSR2, unix.SIGSEGV, unix.SIGCONT:
				forwardSignal(control, sig.(unix.Signal))
			}
		case <-stop:
			return
		}
	}
}

// sendTermSize reads the current terminal size and sends a window-resize
// control message to the incus-agent.
func sendTermSize(control *websocket.Conn) {
	stdoutFd := int(os.Stdout.Fd())
	width, height, err := termios.GetSize(stdoutFd)
	if err != nil {
		return
	}

	msg := api.InstanceExecControl{
		Command: "window-resize",
		Args: map[string]string{
			"width":  fmt.Sprintf("%d", width),
			"height": fmt.Sprintf("%d", height),
		},
	}

	if err := control.WriteJSON(msg); err != nil {
		// Non-fatal: connection may already be closed
	}
}

// forwardSignal sends a signal control message to the incus-agent.
func forwardSignal(control *websocket.Conn, sig unix.Signal) {
	msg := api.InstanceExecControl{
		Command: "signal",
		Signal:  int(sig),
	}

	if err := control.WriteJSON(msg); err != nil {
		// Non-fatal: connection may already be closed
	}
}
