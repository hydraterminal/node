package config

import "time"

// Defaults returns the default configuration.
func Defaults() *Config {
	return &Config{
		Mode: "private",
		Node: NodeConfig{
			ID: "", // Secret is loaded from HYDRA_NODE_SECRET env var
		},
		Network: NetworkConfig{
			BackendURL:         "https://api.hydra.fast",
			SubmissionInterval: 60 * time.Second,
			HeartbeatInterval:  30 * time.Second,
			BatchSize:          100,
		},
		Server: ServerConfig{
			Port: 4001,
			Host: "0.0.0.0",
			RateLimits: RateLimitsConfig{
				Public:        60,
				Authenticated: 300,
			},
		},
		Control: ControlConfig{
			Port: 4002,
			Host: "127.0.0.1",
		},
		Sources: SourcesConfig{
			ADSB:       SourceConfig{Enabled: false, Interval: 60 * time.Second, APIKeyEnv: "ADSB_API_KEY", APIHostEnv: "ADSB_API_HOST"},
			AIS:        SourceConfig{Enabled: false, FlushInterval: 60 * time.Second, APIKeyEnv: "AISSTREAM_API_KEY"},
			Oref:       SourceConfig{Enabled: false, Interval: 3 * time.Second},
			Cyber:      SourceConfig{Enabled: false, Interval: 5 * time.Minute, APIKeyEnv: "OTX_API_KEY"},
			Airspace:   SourceConfig{Enabled: false, Interval: 10 * time.Minute, OpenaipKeyEnv: "OPENAIP_API_KEY"},
			XTracker:   SourceConfig{Enabled: false, Interval: 2 * time.Minute},
			Telegram:   SourceConfig{Enabled: false, Interval: 2 * time.Minute},
			Polymarket: SourceConfig{Enabled: false, MarketInterval: 15 * time.Minute},
			Maritime:   SourceConfig{Enabled: false, Interval: 24 * time.Hour},
			USGS:       SourceConfig{Enabled: true, Interval: 5 * time.Minute},
		},
		Storage: StorageConfig{
			DataDir:        "~/.hydra/data",
			RetentionHours: 24,
		},
		Logging: LoggingConfig{
			Level:           "info",
			Format:          "text",
			StreamToBackend: false,
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Port:    9090,
		},
	}
}
