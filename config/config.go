package config

import (
	"os"
	"strings"
)

// Config holds all configuration for the iws CLI
type Config struct {
	InstanceName string
	Update       bool
	Image        string
	ServerRemote string
	ServerPrefix string
	Help         bool
}

// New creates a new Config with defaults
func New() *Config {
	return &Config{
		InstanceName: getEnv("INST", "workspace"),
		Image:        getEnv("IMAGE", "oci-ghcr:rkoster/workspace:latest"),
	}
}

// ParseArguments processes command-line arguments
func (c *Config) ParseArguments(args []string) error {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--update":
			c.Update = true
		case "--help", "-h":
			c.Update = true // reuse the flag to trigger help display
			c.Help = true
		default:
			// Check if it's a key=value pair
			if strings.Contains(args[i], "=") {
				parts := strings.SplitN(args[i], "=", 2)
				key := parts[0]
				value := parts[1]
				switch key {
				case "image":
					c.Image = value
				case "remote":
					c.ServerRemote = value
				case "inst":
					c.InstanceName = value
				}
			} else {
				// Positional arguments
				if c.Image == getEnv("IMAGE", "oci-ghcr:rkoster/workspace:latest") {
					c.Image = args[i]
				} else if c.ServerRemote == "" {
					c.ServerRemote = args[i]
				}
			}
		}
	}
	return nil
}

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}
