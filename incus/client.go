package incus

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"

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

// ExecCommandWithOutput executes a command in the instance and captures output
func (c *Client) ExecCommandWithOutput(instanceName string, command []string) (string, string, error) {
	var stdout, stderr bytes.Buffer

	err := c.ExecCommand(instanceName, command, nil, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}
