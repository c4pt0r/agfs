package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the entire configuration file
type Config struct {
	Server          ServerConfig            `yaml:"server"`
	Plugins         map[string]PluginConfig `yaml:"plugins"`
	ExternalPlugins ExternalPluginsConfig   `yaml:"external_plugins"`
}

// ServerConfig contains server-level configuration
type ServerConfig struct {
	Address             string `yaml:"address"`
	LogLevel            string `yaml:"log_level"`
	MaxRequestBodyBytes int64  `yaml:"max_request_body_bytes"`
}

// ExternalPluginsConfig contains configuration for external plugins
type ExternalPluginsConfig struct {
	Enabled       bool             `yaml:"enabled"`
	PluginDir     string           `yaml:"plugin_dir"`
	AutoLoad      bool             `yaml:"auto_load"`
	PluginPaths   []string         `yaml:"plugin_paths"`
	WASIMountPath string           `yaml:"wasi_mount_path"` // Directory to mount for WASI filesystem access
	WASM          WASMPluginConfig `yaml:"wasm"`            // WASM plugin specific configuration
}

// WASMPluginConfig contains configuration for WASM plugins
type WASMPluginConfig struct {
	InstancePoolSize     int  `yaml:"instance_pool_size"`     // Maximum concurrent instances per plugin (default: 10)
	InstanceMaxLifetime  int  `yaml:"instance_max_lifetime"`  // Maximum instance lifetime in seconds (0 = unlimited)
	InstanceMaxRequests  int  `yaml:"instance_max_requests"`  // Maximum requests per instance (0 = unlimited)
	HealthCheckInterval  int  `yaml:"health_check_interval"`  // Health check interval in seconds (0 = disabled)
	EnablePoolStatistics bool `yaml:"enable_pool_statistics"` // Enable pool statistics collection
}

// PluginConfig can be either a single plugin or an array of plugin instances
type PluginConfig struct {
	// For single instance plugins
	Enabled bool                   `yaml:"enabled"`
	Path    string                 `yaml:"path"`
	Config  map[string]interface{} `yaml:"config"`

	// For multi-instance plugins (array format)
	Instances []PluginInstance `yaml:"-"`
}

// PluginInstance represents a single instance of a plugin
type PluginInstance struct {
	Name    string                 `yaml:"name"`
	Enabled bool                   `yaml:"enabled"`
	Path    string                 `yaml:"path"`
	Config  map[string]interface{} `yaml:"config"`
}

// UnmarshalYAML implements custom unmarshaling to support both single plugin and array formats
func (p *PluginConfig) UnmarshalYAML(node *yaml.Node) error {
	// Try to unmarshal as array first
	var instances []PluginInstance
	if err := node.Decode(&instances); err == nil && len(instances) > 0 {
		p.Instances = instances
		return nil
	}

	// Otherwise, unmarshal as single plugin config
	type pluginConfigAlias PluginConfig
	aux := (*pluginConfigAlias)(p)
	return node.Decode(aux)
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &cfg, nil
}

// GetPluginConfig returns the configuration for a specific plugin
func (c *Config) GetPluginConfig(pluginName string) (PluginConfig, bool) {
	cfg, ok := c.Plugins[pluginName]
	return cfg, ok
}

// GetWASMConfig returns the WASM plugin configuration with defaults applied
func (c *Config) GetWASMConfig() WASMPluginConfig {
	cfg := c.ExternalPlugins.WASM

	// Apply defaults if not set
	if cfg.InstancePoolSize <= 0 {
		cfg.InstancePoolSize = 10 // Default: 10 concurrent instances
	}
	if cfg.InstanceMaxLifetime < 0 {
		cfg.InstanceMaxLifetime = 0 // Default: unlimited
	}
	if cfg.InstanceMaxRequests < 0 {
		cfg.InstanceMaxRequests = 0 // Default: unlimited
	}
	if cfg.HealthCheckInterval < 0 {
		cfg.HealthCheckInterval = 0 // Default: disabled
	}

	return cfg
}
