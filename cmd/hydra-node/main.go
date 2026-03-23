package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/node"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "hydra-node",
	Short: "Hydra Node — distributed intelligence data collection",
	Long:  "Hydra Node collects, normalizes, and serves real-time OSINT and intelligence data.\nRun as a public node to earn rewards, or as a private node for personal use.",
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Hydra node",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		n, err := node.New(cfg, config.ResolvePath(cfgFile))
		if err != nil {
			return fmt.Errorf("failed to create node: %w", err)
		}
		n.SetVersion(version)

		return n.Run()
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show node status and config",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		fmt.Printf("Mode:       %s\n", cfg.Mode)
		fmt.Printf("Backend:    %s\n", cfg.Network.BackendURL)
		fmt.Printf("Server:     %s:%d\n", cfg.Server.Host, cfg.Server.Port)

		if cfg.Node.ID != "" {
			fmt.Printf("Node ID:    %s\n", cfg.Node.ID)
		} else {
			fmt.Printf("Node ID:    not set\n")
		}
		if cfg.Node.Secret != "" {
			fmt.Printf("Secret:     set (from HYDRA_NODE_SECRET env)\n")
		} else {
			fmt.Printf("Secret:     not set — export HYDRA_NODE_SECRET=hnd_...\n")
		}

		// Count enabled sources
		enabled := 0
		type entry struct {
			name    string
			enabled bool
		}
		sources := []entry{
			{"usgs", cfg.Sources.USGS.Enabled},
			{"adsb", cfg.Sources.ADSB.Enabled},
			{"ais", cfg.Sources.AIS.Enabled},
			{"oref", cfg.Sources.Oref.Enabled},
			{"cyber", cfg.Sources.Cyber.Enabled},
			{"airspace", cfg.Sources.Airspace.Enabled},
			{"telegram", cfg.Sources.Telegram.Enabled},
			{"xtracker", cfg.Sources.XTracker.Enabled},
			{"polymarket", cfg.Sources.Polymarket.Enabled},
			{"maritime", cfg.Sources.Maritime.Enabled},
		}
		var active []string
		for _, s := range sources {
			if s.enabled {
				enabled++
				active = append(active, s.name)
			}
		}
		fmt.Printf("Sources:    %d enabled %v\n", enabled, active)
		return nil
	},
}

var claimRewardsCmd = &cobra.Command{
	Use:   "claim-rewards",
	Short: "Claim earned rewards from the Hydra network",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Reward claiming not yet implemented.")
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default: ~/.hydra/node.yaml)")

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(claimRewardsCmd)
}

// loadDotEnv reads KEY=VALUE pairs from a .env file and sets any that are
// not already present in the environment. Silently skips missing files.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" && os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func main() {
	// Load .env from the same directory as the config file (or cwd).
	// Never overwrites env vars that are already set.
	loadDotEnv(".env")
	if cfgFile != "" {
		loadDotEnv(filepath.Join(filepath.Dir(cfgFile), ".env"))
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
