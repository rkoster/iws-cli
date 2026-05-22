# iws connect Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the broken `COLUMNS/LINES` env var terminal sizing approach with a proper `iws connect` subcommand that uses the Incus Go client's control WebSocket to relay terminal resize events.

**Architecture:** Add a new `iws connect` subcommand that runs inside the Ghostty window. It connects to the Incus server using the Go client (reading TLS certs from `~/.config/incus/`), allocates an interactive PTY with the correct initial size from Ghostty's terminal fd, and relays SIGWINCH → `window-resize` via the control WebSocket.

**Tech Stack:** Go, Incus v7 Go client, `golang.org/x/sys/unix` (SIGWINCH), `github.com/gorilla/websocket`, `github.com/lxc/incus/v7/shared/termios`

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `config/config.go` | Modify | Add `Connect` bool field, parse `connect` subcommand |
| `cmd/cmd.go` | Modify | Route `iws connect` to new handler, skip VM lifecycle |
| `cmd/connect.go` | **Create** | `iws connect` subcommand logic: connect + exec |
| `incus/connect.go` | **Create** | Incus Go client PTY exec with control WebSocket |
| `workspace/init.go` | Modify | `LaunchGhostty` to invoke `iws connect` instead of `incus exec` |

---

### Task 1: Add `connect` subcommand to config

**Files:**
- Modify: `config/config.go:9-21`

- [ ] **Step 1: Add `Connect` field and parse `connect` argument**

Add a `Connect` bool to the `Config` struct and handle `connect` as a subcommand in `ParseArguments`:

```go
type Config struct {
	InstanceName string
	Update       bool
	Destroy      bool
	Connect      bool    // NEW: true when "connect" subcommand is used
	Help         bool
	ServerRemote string
	ServerPrefix string
	CPU          string
	Memory       string
	Disk         string
	NixpkgsPath  string
}
```

In `ParseArguments`, add a case for `connect`:

```go
func (c *Config) ParseArguments(args []string) error {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "connect":
			c.Connect = true
		case "--update":
			c.Update = true
		case "--destroy":
			c.Destroy = true
		case "--help", "-h":
			c.Help = true
		default:
			if strings.Contains(args[i], "=") {
				parts := strings.SplitN(args[i], "=", 2)
				switch parts[0] {
				case "inst":
					c.InstanceName = parts[1]
				case "remote":
					c.ServerRemote = parts[1]
				case "cpu":
					c.CPU = parts[1]
				case "memory":
					c.Memory = parts[1]
				case "disk":
					c.Disk = parts[1]
				}
			}
		}
	}
	return nil
}
```

- [ ] **Step 2: Build to verify no syntax errors**

Run: `cd /Users/Ruben.Koster/workspace/iws-cli && go build ./...`
Expected: clean build, no errors

- [ ] **Step 3: Commit**

```bash
git add config/config.go
git commit -m "config: add Connect subcommand flag"
```

---

### Task 2: Create `incus/connect.go` — Go client PTY exec with control WebSocket

**Files:**
- Create: `incus/connect.go`

This file contains the core logic: connecting to the Incus server via Go client, allocating an interactive PTY, and relaying terminal resize events.

- [ ] **Step 1: Write `incus/connect.go`**

```go
package incus

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"

	"github.com/gorilla/websocket"
	"github.com/lxc/incus/v7/client"
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

	// Determine the remote address
	var addr string
	if remote != "" {
		addr = remote
	} else {
		// Try to find a remote from config
		for name, r := range cfg.Remotes {
			if r.Protocol == "incus" && r.Static {
				addr = r.Addr
				break
			}
		}
		if addr == "" {
			return fmt.Errorf("no Incus remote configured")
		}
	}

	// Build connection args from Incus config (reads TLS certs)
	args, err := cfg.GetConnectionArgs(addr)
	if err != nil {
		return fmt.Errorf("failed to get connection args: %w", err)
	}

	// Connect to the Incus server
	server, err := client.ConnectIncus(addr, args)
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

	// Prepare exec args with stdin/stdout/stderr and control handler
	execArgs := &client.InstanceExecArgs{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Control:  controlSocketHandler,
		DataDone: make(chan bool),
	}

	// Execute the command
	fullInstance := instance
	if remote != "" {
		fullInstance = remote + instance
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

	return nil
}

// controlSocketHandler handles the control WebSocket connection for terminal
// resize (SIGWINCH) and signal forwarding. It mirrors the behavior of the
// incus CLI's exec command.
func controlSocketHandler(control *websocket.Conn) {
	if runtime.GOOS == "windows" {
		// Windows doesn't support SIGWINCH via unix signals
		// Just consume pings
		_, _, _ = control.ReadMessage()
		return
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
		sig := <-ch

		switch sig {
		case unix.SIGWINCH:
			// Send window-resize to the incus-agent
			sendTermSize(control)

		case unix.SIGTERM, unix.SIGHUP, unix.SIGINT, unix.SIGQUIT,
			unix.SIGABRT, unix.SIGTSTP, unix.SIGTTIN, unix.SIGTTOU,
			unix.SIGUSR1, unix.SIGUSR2, unix.SIGSEGV, unix.SIGCONT:
			forwardSignal(control, sig)
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

// ensure io import is used (for potential future use)
var _ = io.EOF
```

- [ ] **Step 2: Build to verify no syntax errors**

Run: `cd /Users/Ruben.Koster/workspace/iws-cli && go build ./...`
Expected: clean build, no errors

- [ ] **Step 3: Commit**

```bash
git add incus/connect.go
git commit -m "incus: add ConnectAndExec with control WebSocket for terminal resize"
```

---

### Task 3: Create `cmd/connect.go` — `iws connect` subcommand handler

**Files:**
- Create: `cmd/connect.go`

- [ ] **Step 1: Write `cmd/connect.go`**

```go
package cmd

import (
	"fmt"
	"os"

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
```

- [ ] **Step 2: Build to verify no syntax errors**

Run: `cd /Users/Ruben.Koster/workspace/iws-cli && go build ./...`
Expected: clean build, no errors

- [ ] **Step 3: Commit**

```bash
git add cmd/connect.go
git commit -m "cmd: add handleConnect for iws connect subcommand"
```

---

### Task 4: Wire `connect` subcommand into `cmd/cmd.go`

**Files:**
- Modify: `cmd/cmd.go:14-120`

- [ ] **Step 1: Add connect routing in Execute()**

After the help check and before the Incus client initialization, add the connect handler:

```go
func Execute() error {
	cfg := config.New()

	if len(os.Args) > 1 {
		if err := cfg.ParseArguments(os.Args[1:]); err != nil {
			return fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	if cfg.Help {
		printHelp()
		return nil
	}

	// Handle "iws connect" subcommand — runs inside Ghostty, no VM lifecycle
	if cfg.Connect {
		return handleConnect(cfg.InstanceName, cfg.ServerRemote)
	}

	// ... rest of Execute() unchanged ...
```

The rest of `Execute()` (destroy, VM lifecycle, `LaunchGhostty` call) stays unchanged for the default `iws` flow.

- [ ] **Step 2: Build to verify no syntax errors**

Run: `cd /Users/Ruben.Koster/workspace/iws-cli && go build ./...`
Expected: clean build, no errors

- [ ] **Step 3: Commit**

```bash
git add cmd/cmd.go
git commit -m "cmd: wire connect subcommand into Execute flow"
```

---

### Task 5: Update `workspace/init.go` — `LaunchGhostty` uses `iws connect`

**Files:**
- Modify: `workspace/init.go`

- [ ] **Step 1: Replace `LaunchGhostty` implementation**

Replace the entire `LaunchGhostty` function. Remove the `termios` import and the `COLUMNS/LINES` approach. The new implementation launches Ghostty with `iws connect` as the command:

```go
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
	_, err := client.IsInstanceRunning(instanceName)
	if err != nil {
		fmt.Printf("Instance '%s' does not exist, nothing to destroy\n", instanceName)
		return nil
	}

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
```

Key changes:
- Removed `os`, `os/exec` (still needed for `getVMIP`), `strings` imports — wait, `os/exec` and `strings` are still needed for `getVMIP`. Keep them.
- Removed `github.com/lxc/incus/v7/shared/termios` import (no longer used)
- Removed terminal size reading and `COLUMNS/LINES` env var passing
- Changed Ghostty command from `incus exec -t ... COLUMNS=... LINES=...` to `iws connect ... --remote '...'`

- [ ] **Step 2: Build to verify no syntax errors**

Run: `cd /Users/Ruben.Koster/workspace/iws-cli && go build ./...`
Expected: clean build, no errors

- [ ] **Step 3: Commit**

```bash
git add workspace/init.go
git commit -m "workspace: replace incus exec with iws connect for proper terminal sizing"
```

---

### Task 6: Update help text and verify full build

**Files:**
- Modify: `cmd/cmd.go:122-146`

- [ ] **Step 1: Update printHelp() to document `connect` subcommand**

```go
func printHelp() {
	fmt.Fprintf(os.Stderr, `Usage: iws [OPTIONS]
Usage: iws connect [OPTIONS]

Launch a NixOS VM workspace and connect via Ghostty.

Commands:
  connect     Connect to an existing workspace VM (run inside Ghostty)

Options:
  --update          Push config and run nixos-rebuild switch
  --destroy         Stop and delete the VM (volumes preserved)
  --help, -h        Show this help message

Configuration:
  inst=NAME         Instance name (default: workspace, env: INST)
  cpu=N             CPU count (default: 4, env: IWS_CPU)
  memory=SIZE       Memory limit (default: 8GiB, env: IWS_MEMORY)
  disk=SIZE         Root disk size (default: 50GiB, env: IWS_DISK)
  remote=URL        Incus server URL (default from ~/.config/incus/)

Config directory: ~/.config/iws/nixpkgs/ (env: IWS_NIXPKGS)

Examples:
  iws                       # Launch or connect to workspace VM
  iws --update              # Re-provision with latest config
  iws --destroy             # Delete VM (keeps volumes)
  iws connect workspace     # Connect to existing VM (from inside Ghostty)
  iws cpu=8 memory=16GiB    # Custom resources
`)
}
```

- [ ] **Step 2: Full build + vet**

Run: `cd /Users/Ruben.Koster/workspace/iws-cli && go build ./... && go vet ./...`
Expected: clean, no errors or warnings

- [ ] **Step 3: Commit**

```bash
git add cmd/cmd.go
git commit -m "cmd: update help text for connect subcommand"
```

---

## Self-Review Checklist

1. **Spec coverage:**
   - `iws connect` subcommand → Task 1 (config), Task 3 (handler), Task 4 (routing) ✓
   - Incus Go client with control WebSocket → Task 2 (connect.go) ✓
   - Authentication from `~/.config/incus/` → Task 2 (`cliconfig.GetConnectionArgs`) ✓
   - SIGWINCH relay → Task 2 (`controlSocketHandler` + `sendTermSize`) ✓
   - tmux starts inside exec → Task 2 (exec command includes tmux) ✓
   - `LaunchGhostty` changes to `iws connect` → Task 5 ✓
   - Terminal size read from Ghostty's fd → Task 2 (`termios.GetSize(os.Stdout.Fd())`) ✓

2. **Placeholder scan:** No "TBD", "TODO", "implement later", or "similar to" patterns found.

3. **Type consistency:** `ConnectAndExec(instance, remote string)` signature matches usage in Task 3 and Task 4. `api.InstanceExecControl` fields match Incus source.

4. **Dependencies:** All imports are from existing dependencies in `go.mod` (`incus/v7`, `gorilla/websocket`, `golang.org/x/sys`).

5. **Edge cases:**
   - `remote` is empty → falls back to finding a static remote from config
   - Terminal size read fails → defaults to 80x24
   - Control WebSocket write fails → non-fatal, logged silently (matches incus CLI behavior)
   - Windows → SIGWINCH handler is a no-op (matches incus CLI)
