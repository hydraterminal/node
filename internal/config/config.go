package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level node configuration.
type Config struct {
	Mode    string        `yaml:"mode" json:"mode"`
	Node    NodeConfig    `yaml:"node" json:"node"`
	Network NetworkConfig `yaml:"network" json:"network"`
	Server  ServerConfig  `yaml:"server" json:"server"`
	Control ControlConfig `yaml:"control" json:"control"`
	Sources SourcesConfig `yaml:"sources" json:"sources"`
	Storage StorageConfig `yaml:"storage" json:"storage"`
	Logging LoggingConfig `yaml:"logging" json:"logging"`
	Metrics MetricsConfig `yaml:"metrics" json:"metrics"`
}

// NodeConfig holds the node identity — set when creating a node on the platform.
type NodeConfig struct {
	ID     string `yaml:"id"`               // node ID from Platform → Nodes → Create Node
	Secret string `yaml:"-" json:"secret"`  // read from HYDRA_NODE_SECRET env var, never stored in YAML
}

type NetworkConfig struct {
	BackendURL         string        `yaml:"backendUrl"`
	SubmissionInterval time.Duration `yaml:"submissionInterval"`
	HeartbeatInterval  time.Duration `yaml:"heartbeatInterval"`
	BatchSize          int           `yaml:"batchSize"`
}

type ServerConfig struct {
	Port       int              `yaml:"port"`
	Host       string           `yaml:"host"`
	Domain     string           `yaml:"domain"`
	RateLimits RateLimitsConfig `yaml:"rateLimits"`
}

type RateLimitsConfig struct {
	Public        int `yaml:"public"`
	Authenticated int `yaml:"authenticated"`
}

type ControlConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type SourceConfig struct {
	Enabled        bool          `yaml:"enabled" json:"enabled"`
	Interval       time.Duration `yaml:"interval,omitempty" json:"-"`
	FlushInterval  time.Duration `yaml:"flushInterval,omitempty" json:"-"`
	APIKeyEnv      string        `yaml:"apiKeyEnv,omitempty" json:"apiKeyEnv,omitempty"`
	APIHostEnv     string        `yaml:"apiHostEnv,omitempty" json:"apiHostEnv,omitempty"`
	OpenaipKeyEnv  string        `yaml:"openaipKeyEnv,omitempty" json:"openaipKeyEnv,omitempty"`
	ProxyURL       string        `yaml:"proxyUrl,omitempty" json:"proxyUrl,omitempty"`
	Accounts       []string      `yaml:"accounts,omitempty" json:"accounts,omitempty"`
	Cookies        []string      `yaml:"cookies,omitempty" json:"cookies,omitempty"`
	Channels       []string      `yaml:"channels,omitempty" json:"channels,omitempty"`
	MarketInterval time.Duration `yaml:"marketInterval,omitempty" json:"-"`
}

// MarshalJSON serializes SourceConfig with durations as human-readable strings.
func (sc SourceConfig) MarshalJSON() ([]byte, error) {
	type Alias SourceConfig
	return json.Marshal(&struct {
		Alias
		Interval       string `json:"interval,omitempty"`
		FlushInterval  string `json:"flushInterval,omitempty"`
		MarketInterval string `json:"marketInterval,omitempty"`
	}{
		Alias:          Alias(sc),
		Interval:       durationStr(sc.Interval),
		FlushInterval:  durationStr(sc.FlushInterval),
		MarketInterval: durationStr(sc.MarketInterval),
	})
}

func durationStr(d time.Duration) string {
	if d == 0 {
		return ""
	}
	return d.String()
}

type SourcesConfig struct {
	ADSB       SourceConfig `yaml:"adsb" json:"adsb"`
	AIS        SourceConfig `yaml:"ais" json:"ais"`
	Oref       SourceConfig `yaml:"oref" json:"oref"`
	Cyber      SourceConfig `yaml:"cyber" json:"cyber"`
	Airspace   SourceConfig `yaml:"airspace" json:"airspace"`
	XTracker   SourceConfig `yaml:"xtracker" json:"xtracker"`
	Telegram   SourceConfig `yaml:"telegram" json:"telegram"`
	Polymarket SourceConfig `yaml:"polymarket" json:"polymarket"`
	Maritime   SourceConfig `yaml:"maritime" json:"maritime"`
	USGS       SourceConfig `yaml:"usgs" json:"usgs"`
}

type StorageConfig struct {
	DataDir        string `yaml:"dataDir"`
	RetentionHours int    `yaml:"retentionHours"`
}

type LoggingConfig struct {
	Level           string `yaml:"level"`
	Format          string `yaml:"format"`
	StreamToBackend bool   `yaml:"streamToBackend"`
}

type MetricsConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

// Load reads the config from the given path, falling back to defaults.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return cfg, nil
		}
		path = filepath.Join(home, ".hydra", "node.yaml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Expand ~ in data dir
	if strings.HasPrefix(cfg.Storage.DataDir, "~") {
		home, _ := os.UserHomeDir()
		cfg.Storage.DataDir = filepath.Join(home, cfg.Storage.DataDir[1:])
	}

	// Node secret is always read from environment, never from YAML
	if secret := os.Getenv("HYDRA_NODE_SECRET"); secret != "" {
		cfg.Node.Secret = secret
	}

	return cfg, nil
}

// IsPublic returns true if the node is in public mode.
func (c *Config) IsPublic() bool {
	return c.Mode == "public"
}

// GetNodeSecret returns the shared secret for backend auth.
func (c *Config) GetNodeSecret() string {
	return c.Node.Secret
}

// GetNodeID returns the node ID.
func (c *Config) GetNodeID() string {
	return c.Node.ID
}

// ResolvePath returns the absolute path to the config file.
// If path is empty, returns the default ~/.hydra/node.yaml.
func ResolvePath(path string) string {
	if path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "node.yaml"
	}
	return filepath.Join(home, ".hydra", "node.yaml")
}

// Save writes the config to the given path as YAML.
func Save(cfg *Config, path string) error {
	path = ResolvePath(path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// ReadRaw reads the config file as raw YAML text.
func ReadRaw(path string) (string, error) {
	path = ResolvePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return marshalled defaults
			cfg := Defaults()
			out, _ := yaml.Marshal(cfg)
			return string(out), nil
		}
		return "", fmt.Errorf("read config: %w", err)
	}
	return string(data), nil
}

// SaveRaw writes raw YAML text to the config file.
func SaveRaw(yamlContent string, path string) error {
	// Validate it's parseable YAML first
	var test Config
	if err := yaml.Unmarshal([]byte(yamlContent), &test); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	path = ResolvePath(path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// GetSourceAPIKey reads an API key from the environment for a source.
func GetSourceAPIKey(envName string) string {
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
}
