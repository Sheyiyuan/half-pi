// Package config handles YAML configuration loading.
package config

// Config holds the half-pi mind configuration.
type Config struct {
	DataDir string `yaml:"data_dir"`
	LogDir  string `yaml:"log_dir"`
}

// Load reads configuration from path.
func Load(path string) (*Config, error) {
	return &Config{}, nil
}
