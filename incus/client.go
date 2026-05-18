package incus

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/cliconfig"
)

// Client handles Incus operations using the official Go client
type Client struct {
	RemoteClient incusclient.InstanceServer
	RemoteName   string
	LocalClient  incusclient.InstanceServer
	Config       *cliconfig.Config
}

// New creates a new Incus client
func New() (*Client, error) {
	c := &Client{}

	// Load Incus config
	var err error
	c.Config, err = cliconfig.LoadConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to load Incus config: %w", err)
	}

	// Try to connect to a remote server first
	remoteClient, err := c.connectToRemoteServer()
	if err == nil && remoteClient != nil {
		c.RemoteClient = remoteClient
		return c, nil
	}

	// Fallback to local client
	if runtime.GOOS == "linux" {
		localClient, err := incusclient.ConnectIncusUnix("", nil)
		if err == nil {
			c.LocalClient = localClient
			return c, nil
		}
	}

	// If we have a config but couldn't connect, return the config for later use
	if c.Config != nil {
		return c, nil
	}

	return c, fmt.Errorf("no Incus connection available (remote or local)")
}

// connectToRemoteServer finds and connects to a remote Incus server
func (c *Client) connectToRemoteServer() (incusclient.InstanceServer, error) {
	// Find a server remote (incus protocol, not static)
	for name, details := range c.Config.Remotes {
		if details.Protocol != "incus" || details.Static {
			continue
		}

		// Connect to this remote
		conn := &incusclient.ConnectionArgs{}

		// Try to get the server certificate
		certPath := c.Config.ServerCertPath(name)
		if certData, err := os.ReadFile(certPath); err == nil {
			conn.TLSServerCert = string(certData)
		}

		// Try to get client certificates
		if cert, key, ca, err := c.Config.GetClientCertificate(name); err == nil {
			conn.TLSClientCert = cert
			conn.TLSClientKey = key
			conn.TLSCA = ca
		}

		remoteClient, err := incusclient.ConnectIncus(details.Addr, conn)
		if err == nil {
			c.RemoteName = name
			c.RemoteClient = remoteClient
			return remoteClient, nil
		}

		// If that fails, try without certificates (for testing)
		connNoCert := &incusclient.ConnectionArgs{
			InsecureSkipVerify: true,
		}
		remoteClient, err = incusclient.ConnectIncus(details.Addr, connNoCert)
		if err == nil {
			c.RemoteName = name
			c.RemoteClient = remoteClient
			return remoteClient, nil
		}
	}

	return nil, fmt.Errorf("no suitable remote server found")
}

// ConfigureGHCRRemote ensures the GHCR remote is configured
func (c *Client) ConfigureGHCRRemote() error {
	if _, exists := c.Config.Remotes["oci-ghcr"]; exists {
		return nil // Remote already exists
	}

	fmt.Println("Configuring incus remote 'oci-ghcr' for GitHub Container Registry...")

	c.Config.Remotes["oci-ghcr"] = cliconfig.Remote{
		Protocol: "oci",
		Public:   true,
		Addr:     "https://ghcr.io",
	}

	return c.Config.SaveConfig("")
}

// GetServerRemote returns the name of the detected server remote
func (c *Client) GetServerRemote() string {
	return c.RemoteName
}

// GetClient returns the primary InstanceServer (remote if available, local otherwise)
func (c *Client) GetClient() incusclient.InstanceServer {
	if c.RemoteClient != nil {
		return c.RemoteClient
	}
	return c.LocalClient
}

// PullImage copies the requested image into the target
func (c *Client) PullImage(image string, alias string) (string, error) {
	fmt.Printf("Pulling latest image '%s' into target...\n", image)

	targetClient := c.GetClient()

	// Parse the image reference
	var sourceServer, sourceAlias string
	if len(image) > 0 && image[0] == '/' {
		// Local image
		sourceServer = ""
		sourceAlias = image[1:]
	} else {
		// Remote image
		parts := splitImageRef(image)
		sourceServer = parts[0]
		sourceAlias = parts[1]
	}

	// Check if this is an OCI image
	isOCI := false
	if sourceServer != "" {
		if remote, exists := c.Config.Remotes[sourceServer]; exists {
			if remote.Protocol == "oci" {
				isOCI = true
			}
		}
	}

	// For OCI images, use the Incus CLI since the Go client has authentication issues
	if isOCI {
		fmt.Println("Pulling OCI image using Incus CLI...")
		
		// Use incus image copy to pull the OCI image
		serverRemote := c.GetServerRemote()
		if serverRemote == "" {
			serverRemote = "local"
		}
		
		// Delete existing alias if it exists
		deleteCmd := exec.Command("incus", "image", "alias", "delete", serverRemote+":"+alias)
		deleteCmd.Run() // Ignore error
		
		cmd := exec.Command("incus", "image", "copy", image, serverRemote+":", "--alias", alias)
		
		// Set up environment to use the Incus config
		if c.Config.ConfigDir != "" {
			cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
		}
		
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("failed to pull OCI image: %w: %s", err, string(output))
		}

		// Get the pulled image fingerprint
		images, err := targetClient.GetImages()
		if err != nil {
			return "", fmt.Errorf("failed to list images: %w", err)
		}

		var pulledFingerprint string
		for _, img := range images {
			if img.Properties != nil && img.Properties["id"] == image {
				pulledFingerprint = img.Fingerprint
				break
			}
		}

		if pulledFingerprint == "" {
			// Fallback: use the most recent image
			if len(images) > 0 {
				pulledFingerprint = images[0].Fingerprint
			} else {
				return "", fmt.Errorf("no image found after pull")
			}
		}

		return pulledFingerprint, nil
	}

	// For non-OCI images, use the Go client
	if sourceServer != "" {
		// Connect to source server
		sourceClient, err := c.connectToServer(sourceServer)
		if err != nil {
			return "", fmt.Errorf("failed to connect to source server: %w", err)
		}

		// Get the image from source
		imageInfo, _, err := sourceClient.GetImage(sourceAlias)
		if err != nil {
			return "", fmt.Errorf("failed to get image from source: %w", err)
		}

		// Copy image to target
		copyArgs := &incusclient.ImageCopyArgs{
			Aliases: []api.ImageAlias{
				{Name: alias},
			},
			CopyAliases: false,
		}

		rop, err := targetClient.CopyImage(sourceClient, *imageInfo, copyArgs)
		if err != nil {
			return "", fmt.Errorf("failed to copy image: %w", err)
		}

		if err := rop.Wait(); err != nil {
			return "", fmt.Errorf("failed to wait for image copy: %w", err)
		}

		return imageInfo.Fingerprint, nil
	}

	// Local image - just set the alias
	imageInfo, _, err := targetClient.GetImage(sourceAlias)
	if err != nil {
		return "", fmt.Errorf("failed to get local image: %w", err)
	}

	// Set the alias
	aliasEntry := api.ImageAliasesPost{
		ImageAliasesEntry: api.ImageAliasesEntry{
			Name: alias,
			Type: "container",
		},
	}

	err = targetClient.CreateImageAlias(aliasEntry)
	if err != nil {
		return "", fmt.Errorf("failed to create image alias: %w", err)
	}

	return imageInfo.Fingerprint, nil
}

func (c *Client) connectToServer(serverName string) (incusclient.ImageServer, error) {
	remote, exists := c.Config.Remotes[serverName]
	if !exists {
		// Try as a URL
		return incusclient.ConnectSimpleStreams("https://"+serverName, nil)
	}

	// Check if it's an OCI registry
	if remote.Protocol == "oci" {
		return incusclient.ConnectOCI(remote.Addr, nil)
	}

	return incusclient.ConnectIncus(remote.Addr, nil)
}

// LaunchSystemContainer creates and starts a system container using the incus CLI.
// Unlike CreateInstanceFromImage (Go API), this ensures the container boots with
// init/systemd instead of running as an app container.
func (c *Client) LaunchSystemContainer(imageRef, instanceName, pool string) error {
	fmt.Printf("Launching %s\n", instanceName)

	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}

	// Determine storage pool
	if pool == "" {
		var err error
		pool, err = c.DetectStoragePool()
		if err != nil {
			return err
		}
	}

	// Create storage volumes if they don't exist
	if err := c.CreateVolumeIfNotExists(pool, "workspace-config"); err != nil {
		return err
	}
	if err := c.CreateVolumeIfNotExists(pool, "workspace"); err != nil {
		return err
	}

	// Use incus init + device add + start (incus launch -d only overrides profile devices)
	initArgs := []string{
		"init",
		imageRef,
		instanceName,
		"-c", "security.nesting=true",
		"-n", "incusbr0",
	}

	if pool != "local" {
		initArgs = append(initArgs, "-s", pool)
	}

	cmd := exec.Command("incus", initArgs...)
	if c.Config.ConfigDir != "" {
		cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to init container: %w: %s", err, string(output))
	}

	// Add disk devices for persistent volumes
	devices := []struct {
		name, pool, source, path string
	}{
		{"config", pool, "workspace-config", "/home/ruben/.config-volume"},
		{"workspace", pool, "workspace", "/home/ruben/workspace"},
	}

	for _, dev := range devices {
		addCmd := exec.Command("incus", "config", "device", "add", instanceName,
			dev.name, "disk",
			"pool="+dev.pool,
			"source="+dev.source,
			"path="+dev.path,
		)
		if c.Config.ConfigDir != "" {
			addCmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
		}
		output, err = addCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to add device %s: %w: %s", dev.name, err, string(output))
		}
	}

	// Start the container
	startCmd := exec.Command("incus", "start", instanceName)
	if c.Config.ConfigDir != "" {
		startCmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
	}
	output, err = startCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start container: %w: %s", err, string(output))
	}

	return nil
}

// WaitForSystemdReady waits for systemd to be fully booted in the container.
// Returns when systemctl is-system-running returns "running" or "degraded".
// Times out after maxAttempts * pollInterval duration.
func (c *Client) WaitForSystemdReady(instanceName string, maxAttempts int, pollInterval time.Duration) error {
	fmt.Println("Waiting for systemd to be ready...")

	for i := 0; i < maxAttempts; i++ {
		cmd := exec.Command("incus", "exec", instanceName, "--", "systemctl", "is-system-running")

		if c.Config.ConfigDir != "" {
			cmd.Env = append(os.Environ(), "INCUS_DIR="+c.Config.ConfigDir)
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			if i%10 == 0 || i == maxAttempts-1 {
				fmt.Printf("Waiting for systemd... (attempt %d/%d)\n", i+1, maxAttempts)
			}
			time.Sleep(pollInterval)
			continue
		}

		status := strings.TrimSpace(string(output))
		if status == "running" || status == "degraded" {
			fmt.Printf("Systemd is %s\n", status)
			return nil
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timed out waiting for systemd to be ready")
}

// AttachVolume attaches a storage volume to an instance
func (c *Client) AttachVolume(instanceName, deviceName, pool, sourceVolume, path string) error {
	targetClient := c.GetClient()

	put := api.InstancePut{
		Devices: api.DevicesMap{
			deviceName: {
				"type":   "disk",
				"pool":   pool,
				"source": sourceVolume,
				"path":   path,
			},
		},
	}

	op, err := targetClient.UpdateInstance(instanceName, put, "")
	if err != nil {
		return fmt.Errorf("failed to attach volume: %w", err)
	}

	if err := op.Wait(); err != nil {
		return fmt.Errorf("failed to wait for volume attachment: %w", err)
	}

	return nil
}

// StartInstance starts a stopped instance
func (c *Client) StartInstance(instanceName string) error {
	targetClient := c.GetClient()

	reqState := api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}

	op, err := targetClient.UpdateInstanceState(instanceName, reqState, "")
	if err != nil {
		return fmt.Errorf("failed to start instance: %w", err)
	}

	if err := op.Wait(); err != nil {
		return fmt.Errorf("failed to wait for instance start: %w", err)
	}

	return nil
}

// IsInstanceRunning checks if an instance is running
func (c *Client) IsInstanceRunning(instanceName string) (bool, error) {
	targetClient := c.GetClient()

	state, _, err := targetClient.GetInstanceState(instanceName)
	if err != nil {
		return false, err
	}

	return state.Status == "Running", nil
}

// ExecCommand executes a command in the instance
func (c *Client) ExecCommand(instanceName string, command []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	targetClient := c.GetClient()

	req := api.InstanceExecPost{
		Command:     command,
		WaitForWS:   true,
		Interactive: false,
	}

	args := &incusclient.InstanceExecArgs{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}

	op, err := targetClient.ExecInstance(instanceName, req, args)
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	if err := op.Wait(); err != nil {
		return fmt.Errorf("command execution failed: %w", err)
	}

	return nil
}

// DestroyInstance removes an existing instance
func (c *Client) DestroyInstance(instanceName string) error {
	targetClient := c.GetClient()

	op, err := targetClient.DeleteInstance(instanceName)
	if err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	// Wait for deletion
	for i := 0; i < 60; i++ {
		if err := op.Wait(); err == nil {
			return nil
		}

		// Check if instance still exists
		_, _, err := targetClient.GetInstance(instanceName)
		if err != nil {
			return nil // Instance no longer exists
		}

		fmt.Printf("Waiting for instance '%s' to be removed... (attempt %d)\n", instanceName, i+1)
	}

	return fmt.Errorf("timed out waiting for instance deletion")
}

// DetectStoragePool returns the name of the storage pool
func (c *Client) DetectStoragePool() (string, error) {
	targetClient := c.GetClient()

	pools, err := targetClient.GetStoragePools()
	if err != nil {
		return "local", nil
	}

	for _, pool := range pools {
		if pool.Name == "local" {
			return "local", nil
		}
	}

	if len(pools) > 0 {
		return pools[0].Name, nil
	}

	return "local", nil
}

// CreateVolumeIfNotExists creates a storage volume if it doesn't exist
func (c *Client) CreateVolumeIfNotExists(pool, volumeName string) error {
	targetClient := c.GetClient()

	// Check if volume exists
	_, _, err := targetClient.GetStoragePoolVolume(pool, "custom", volumeName)
	if err == nil {
		return nil // Volume already exists
	}

	// Create the volume
	volume := api.StorageVolumesPost{
		Name: volumeName,
		Type: "custom",
	}

	err = targetClient.CreateStoragePoolVolume(pool, volume)
	if err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}

	fmt.Printf("Creating %s volume on pool '%s'...\n", volumeName, pool)
	return nil
}

// CreateImageAlias creates an image alias
func (c *Client) CreateImageAlias(alias, fingerprint string) error {
	targetClient := c.GetClient()

	aliasEntry := api.ImageAliasesPost{
		ImageAliasesEntry: api.ImageAliasesEntry{
			Name: alias,
			Type: "container",
		},
	}

	return targetClient.CreateImageAlias(aliasEntry)
}

// ExecCommandWithOutput executes a command in the instance and captures output
func (c *Client) ExecCommandWithOutput(instanceName string, command []string) (string, string, error) {
	var stdout, stderr bytes.Buffer

	err := c.ExecCommand(instanceName, command, nil, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func splitImageRef(ref string) []string {
	// Parse image reference like "oci-ghcr:rkoster/workspace:latest"
	// Format: server:alias or just alias
	// The server name is before the first colon that's not part of :// or :tag
	parts := []string{}
	
	// Find the first colon that separates server from alias
	// We need to find the first colon that's not followed by // or a tag name
	colonIdx := -1
	for i := 0; i < len(ref); i++ {
		if ref[i] == ':' {
			// Skip :// (URL protocol)
			if i+2 < len(ref) && ref[i+1:i+3] == "//" {
				continue
			}
			// This colon separates server from alias
			colonIdx = i
			break
		}
	}
	
	if colonIdx > 0 {
		parts = append(parts, ref[:colonIdx])
		parts = append(parts, ref[colonIdx+1:])
	} else {
		parts = append(parts, "", ref)
	}
	
	return parts
}
