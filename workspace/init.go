package workspace

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/ruben-koster/iws-cli/incus"
)

// Config contains the configuration for workspace initialization
type Config struct {
	InstanceName string
	Remote       string
}

// InitConfig initializes the config volume with symlinks
func (w *Config) InitConfig(client *incus.Client, instanceName, remote string) error {
	// The client is already connected to the correct server, so we don't need the server prefix
	targetInstance := instanceName

	// Wait for the instance to be running
	for i := 0; i < 30; i++ {
		running, err := client.IsInstanceRunning(targetInstance)
		if err != nil {
			return fmt.Errorf("failed to check instance status: %w", err)
		}
		if running {
			break
		}
		fmt.Printf("Waiting for instance to be running... (attempt %d)\n", i+1)
		time.Sleep(2 * time.Second)
	}

	// Verify instance is running
	isRunning, err := client.IsInstanceRunning(targetInstance)
	if err != nil {
		return fmt.Errorf("failed to check instance status: %w", err)
	}
	if !isRunning {
		return fmt.Errorf("instance '%s' is not running", instanceName)
	}

	command := `
		for script in /etc/profile.d/*.sh; do
			[ -f "$script" ] && . "$script"
		done

		init_symlink() {
			target="$1"
			link="$2"
			[ -d "$target" ] || mkdir -p "$target"

			if [ -L "$link" ]; then
				if [ "$(readlink "$link")" != "$target" ]; then
					rm -f "$link" || true
					ln -s "$target" "$link" || true
				fi
			else
				if [ -e "$link" ]; then
					mv "$link" "$target" 2>/dev/null || true
				fi
				mkdir -p "$(dirname "$link")" || true
				ln -s "$target" "$link" || true
			fi
		}

		init_config_symlinks() {
			names="gh gh-copilot github-copilot incus"
			mkdir -p /home/ruben/.config || true
			for name in $names; do
				init_symlink /home/ruben/.config-volume/$name /home/ruben/.config/$name
			done
		}

		init_state_symlinks() {
			init_symlink /home/ruben/.config-volume/opencode-data /home/ruben/.local/share/opencode
			init_symlink /home/ruben/.config-volume/opencode-state /home/ruben/.local/state/opencode
			init_symlink /home/ruben/.config-volume/gh-state /home/ruben/.local/state/gh

			target=/home/ruben/.config-volume/zsh_history
			link=/home/ruben/.zsh_history
			[ -f "$target" ] || touch "$target"
			if [ -L "$link" ]; then
				if [ "$(readlink "$link")" != "$target" ]; then
					rm -f "$link" || true
					ln -s "$target" "$link" || true
				fi
			else
				if [ -f "$link" ]; then
					cat "$link" >> "$target" 2>/dev/null || true
					rm -f "$link" || true
				fi
				ln -s "$target" "$link" || true
			fi
		}

		init_config_symlinks
		init_state_symlinks
		mkdir -p /home/ruben/workspace || true
		chown -R 1000:1000 /home/ruben/.config-volume /home/ruben/workspace || true
	`

	var stdout, stderr bytes.Buffer
	err = client.ExecCommand(targetInstance, []string{"/bin/sh", "-c", command}, nil, &stdout, &stderr)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	fmt.Println("Initializing config volume...")
	return nil
}

// AttachVolumes attaches storage volumes to the instance
func (w *Config) AttachVolumes(client *incus.Client, instanceName, remote, pool string) error {
	// The client is already connected to the correct server, so we don't need the server prefix
	targetInstance := instanceName

	// Attach config volume
	fmt.Println("Attaching config volume to instance...")
	if err := client.AttachVolume(targetInstance, "config", pool, "workspace-config", "/home/ruben/.config-volume"); err != nil {
		return fmt.Errorf("failed to attach config volume: %w", err)
	}

	// Attach workspace volume
	fmt.Println("Attaching workspace volume to instance at ~/workspace...")
	if err := client.AttachVolume(targetInstance, "workspace", pool, "workspace", "/home/ruben/workspace"); err != nil {
		return fmt.Errorf("failed to attach workspace volume: %w", err)
	}

	return nil
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

	// The command that runs inside the container to start tmux
	containerCommand := `for s in /etc/profile.d/*.sh; do [ -f "$s" ] && . "$s"; done; for p in /nix/store/*-home-manager-path/bin; do export PATH="$p:$PATH"; done; if command -v zsh >/dev/null 2>&1; then TERM=xterm-256color exec zsh -lc "exec tmux new-session -A -s main"; else TERM=xterm-256color exec /bin/sh -c "exec tmux new-session -A -s main"; fi`

	// Build the full command string for Ghostty's --command flag
	// --command must be a single argument with the full command as its value
	ghosttyCmd := fmt.Sprintf("incus exec %s -- /bin/sh -c '%s'", targetInstance, containerCommand)

	// Launch Ghostty with the incus exec command
	// Ghostty will open a new window and run the command inside it
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
