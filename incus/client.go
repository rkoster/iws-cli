package incus

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/lxc/incus/v7/shared/cliconfig"
)

// Client handles Incus operations using CLI commands
type Client struct {
	RemoteName string
	Config     *cliconfig.Config
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

	// Find the remote name (don't connect yet — connection is lazy via CLI commands)
	for name, details := range c.Config.Remotes {
		if details.Protocol != "incus" || details.Static {
			continue
		}
		c.RemoteName = name
		break
	}

	return c, nil
}

// GetServerRemote returns the name of the detected server remote
func (c *Client) GetServerRemote() string {
	return c.RemoteName
}

// StartInstance starts a stopped instance
func (c *Client) StartInstance(instanceName string) error {
	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	cmd := exec.Command("incus", "start", remoteInstance)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start instance: %w: %s", err, string(out))
	}
	return nil
}

// IsInstanceRunning checks if an instance is running
func (c *Client) IsInstanceRunning(instanceName string) (bool, error) {
	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	cmd := exec.Command("incus", "list", remoteInstance, "--format=csv", "-c", "s")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("instance not found: %w: %s", err, string(output))
	}

	status := strings.TrimSpace(string(output))
	return status == "RUNNING", nil
}

// DestroyInstance removes an existing instance
func (c *Client) DestroyInstance(instanceName string) error {
	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	cmd := exec.Command("incus", "delete", remoteInstance, "--force")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to delete instance: %w: %s", err, string(out))
	}
	return nil
}

// DetectStoragePool returns the name of the storage pool
func (c *Client) DetectStoragePool() (string, error) {
	serverRemote := c.GetServerRemote()
	target := ""
	if serverRemote != "" {
		target = serverRemote + ":"
	}

	cmd := exec.Command("incus", "storage", "list", target, "--format=csv", "-c", "n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "local", nil
	}

	pools := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, pool := range pools {
		if strings.TrimSpace(pool) == "local" {
			return "local", nil
		}
	}

	if len(pools) > 0 && pools[0] != "" {
		return strings.TrimSpace(pools[0]), nil
	}

	return "local", nil
}

// CreateVolumeIfNotExists creates a storage volume if it doesn't exist
func (c *Client) CreateVolumeIfNotExists(pool, volumeName string) error {
	serverRemote := c.GetServerRemote()
	target := ""
	if serverRemote != "" {
		target = serverRemote + ":"
	}

	// Check if volume exists
	cmd := exec.Command("incus", "storage", "volume", "show", target+pool, volumeName)
	if cmd.Run() == nil {
		return nil // Volume already exists
	}

	// Create the volume
	createCmd := exec.Command("incus", "storage", "volume", "create", target+pool, volumeName)
	if out, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create volume: %w: %s", err, string(out))
	}

	fmt.Printf("Created %s volume on pool '%s'\n", volumeName, pool)
	return nil
}

// CreateBlockVolumeIfNotExists creates a block storage volume if it doesn't exist
func (c *Client) CreateBlockVolumeIfNotExists(pool, volumeName, size string) error {
	serverRemote := c.GetServerRemote()
	target := ""
	if serverRemote != "" {
		target = serverRemote + ":"
	}

	// Check if volume exists
	cmd := exec.Command("incus", "storage", "volume", "show", target+pool, volumeName)
	if cmd.Run() == nil {
		return nil // Volume already exists
	}

	// Create the block volume
	createCmd := exec.Command("incus", "storage", "volume", "create", target+pool, volumeName, "--type=block", "size="+size)
	if out, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create block volume: %w: %s", err, string(out))
	}

	fmt.Printf("Created block volume %s (%s) on pool '%s'\n", volumeName, size, pool)
	return nil
}

// FormatConfigVolume formats the config block volume with ext4 if not already formatted
func (c *Client) FormatConfigVolume(instanceName string) error {
	serverRemote := c.GetServerRemote()
	if serverRemote == "" {
		serverRemote = "local"
	}
	remoteInstance := serverRemote + ":" + instanceName

	// Check if already formatted (blkid returns 0 if filesystem detected)
	checkCmd := exec.Command("incus", "exec", remoteInstance, "--", "blkid", "/dev/sda")
	if checkCmd.Run() == nil {
		return nil // Already formatted
	}

	// Format as ext4 with label
	fmt.Println("Formatting config volume as ext4...")
	fmtCmd := exec.Command("incus", "exec", remoteInstance, "--",
		"mkfs.ext4", "-L", "config-vol", "/dev/sda")
	if out, err := fmtCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to format config volume: %w: %s", err, string(out))
	}

	return nil
}
