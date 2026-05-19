package config

import (
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	InstanceName string
	Update       bool
	Destroy      bool
	Help         bool
	ServerRemote string
	ServerPrefix string
	// VM resources
	CPU         string
	Memory      string
	Disk        string
	NixpkgsPath string
}

func New() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		InstanceName: getEnv("INST", "workspace"),
		CPU:          getEnv("IWS_CPU", "4"),
		Memory:       getEnv("IWS_MEMORY", "8GiB"),
		Disk:         getEnv("IWS_DISK", "50GiB"),
		NixpkgsPath:  getEnv("IWS_NIXPKGS", filepath.Join(home, ".config", "iws", "nixpkgs")),
	}
}

func (c *Config) ParseArguments(args []string) error {
	for i := 0; i < len(args); i++ {
		switch args[i] {
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

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}
