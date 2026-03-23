package node

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hydraterminal/node/internal/api"
	"github.com/hydraterminal/node/internal/auth"
	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/control"
	"github.com/hydraterminal/node/internal/metrics"
	"github.com/hydraterminal/node/internal/network"
	"github.com/hydraterminal/node/internal/scheduler"
	"github.com/hydraterminal/node/internal/source"
	"github.com/hydraterminal/node/internal/store"
	"github.com/hydraterminal/node/internal/stream"

	// Import all sources to trigger their init() registrations
	_ "github.com/hydraterminal/node/internal/sources/usgs"
	_ "github.com/hydraterminal/node/internal/sources/cyber"
	_ "github.com/hydraterminal/node/internal/sources/adsb"
	_ "github.com/hydraterminal/node/internal/sources/airspace"
	_ "github.com/hydraterminal/node/internal/sources/oref"
	_ "github.com/hydraterminal/node/internal/sources/ais"
	_ "github.com/hydraterminal/node/internal/sources/telegram"
	_ "github.com/hydraterminal/node/internal/sources/xtracker"
	_ "github.com/hydraterminal/node/internal/sources/polymarket"
	_ "github.com/hydraterminal/node/internal/sources/maritime"
)

// Node is the main Hydra node process.
type Node struct {
	cfg          *config.Config
	configPath   string
	logger       *slog.Logger
	logBuffer    *control.LogBuffer
	store        *store.SQLiteStore
	broadcaster  *stream.Broadcaster
	scheduler    *scheduler.Scheduler
	apiServer    *api.Server
	ctrlServer   *control.Server
	keyValidator *auth.KeyValidator

	// System metrics
	metricsCollector *metrics.Collector

	// Network (public mode only)
	nodeAuth  *network.NodeAuth
	submitter *network.Submitter
	heartbeat *network.Heartbeat

	dataCh  chan source.CollectedBatch
	version string
}

// SetVersion sets the build version string.
func (n *Node) SetVersion(v string) { n.version = v }

// New creates a new Node from the given configuration.
func New(cfg *config.Config, configPath ...string) (*Node, error) {
	level := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	// Create log buffer early so the logger can tee into it.
	logBuffer := control.NewLogBuffer(1000)

	var baseHandler slog.Handler
	if cfg.Logging.Format == "json" {
		baseHandler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		baseHandler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}

	// Wrap with buffer handler so all log output feeds into the live log stream.
	handler := control.NewBufferHandler(baseHandler, logBuffer)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	cfgPath := ""
	if len(configPath) > 0 {
		cfgPath = configPath[0]
	}

	return &Node{
		cfg:        cfg,
		configPath: cfgPath,
		logger:     logger,
		logBuffer:  logBuffer,
		dataCh:     make(chan source.CollectedBatch, 256),
	}, nil
}

// Run starts the node and blocks until shutdown.
func (n *Node) Run() error {
	fmt.Printf("Hydra Node %s\n", n.version)
	n.logger.Info("starting Hydra node",
		"version", n.version,
		"mode", n.cfg.Mode,
		"server", fmt.Sprintf("%s:%d", n.cfg.Server.Host, n.cfg.Server.Port),
	)

	// Initialize store
	var err error
	n.store, err = store.NewSQLiteStore(n.cfg.Storage.DataDir, n.cfg.Storage.RetentionHours)
	if err != nil {
		return fmt.Errorf("init store: %w", err)
	}
	if err := n.store.Init(); err != nil {
		return fmt.Errorf("init store tables: %w", err)
	}

	// Initialize broadcaster
	n.broadcaster = stream.NewBroadcaster(n.logger)
	go n.broadcaster.Run()

	// Initialize API key validator
	n.keyValidator = auth.NewKeyValidator(n.cfg.Network.BackendURL, n.logger)
	n.keyValidator.SetLocalStore(n.store)

	// Initialize enabled sources
	sources, err := n.initSources()
	if err != nil {
		return fmt.Errorf("init sources: %w", err)
	}

	// Initialize scheduler
	n.scheduler = scheduler.New(sources, n.dataCh, n.logger)

	// Initialize API server
	n.apiServer = api.NewServer(n.store, n.broadcaster, n.keyValidator, n.logger)
	n.apiServer.SetVersion(n.version)

	// System metrics collector — samples CPU, memory, network every 5s
	n.metricsCollector = metrics.New(5 * time.Second)
	n.metricsCollector.Start()

	// Initialize control plane and mount on the API server.
	// Control routes are under /control/* on the same port, protected by the node secret.
	// Pass the pre-created logBuffer so it receives all slog output.
	n.ctrlServer = control.NewServerWithBuffer(n.scheduler, n.logger, n.restart, n.cfg.Node.Secret, n.logBuffer, n.store, n.metricsCollector, n.cfg, n.configPath)
	n.ctrlServer.SetVersion(n.version)
	n.apiServer.MountControl(n.ctrlServer)

	// Public mode: setup network auth, submitter, heartbeat
	if n.cfg.IsPublic() {
		if err := n.initNetwork(); err != nil {
			n.logger.Warn("network init failed (running without submissions)", "error", err)
		}
	}

	// Start processing pipeline
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Start data consumer (store + broadcast + submit)
	wg.Add(1)
	go func() {
		defer wg.Done()
		n.consumeData(ctx)
	}()

	// Start scheduler (sources)
	n.scheduler.Start(ctx)

	// Start API server (serves both client API and control plane)
	apiAddr := fmt.Sprintf("%s:%d", n.cfg.Server.Host, n.cfg.Server.Port)
	apiSrv := &http.Server{Addr: apiAddr, Handler: n.apiServer.Handler()}
	wg.Add(1)
	go func() {
		defer wg.Done()
		n.logger.Info("server listening", "addr", apiAddr)
		if err := apiSrv.ListenAndServe(); err != http.ErrServerClosed {
			n.logger.Error("server error", "error", err)
		}
	}()

	// Start periodic tasks
	if n.cfg.IsPublic() && n.submitter != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.runSubmissionLoop(ctx)
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			n.runHeartbeatLoop(ctx)
		}()
	}

	// Start prune loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		n.runPruneLoop(ctx)
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	n.logger.Info("shutting down...")
	cancel()

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	apiSrv.Shutdown(shutdownCtx)
	n.scheduler.Stop()
	n.store.Close()

	wg.Wait()
	n.logger.Info("node stopped")
	return nil
}

// initSources creates and initializes all enabled sources.
func (n *Node) initSources() ([]source.Source, error) {
	type sourceEntry struct {
		name string
		cfg  config.SourceConfig
	}

	entries := []sourceEntry{
		{"usgs", n.cfg.Sources.USGS},
		{"cyber", n.cfg.Sources.Cyber},
		{"adsb", n.cfg.Sources.ADSB},
		{"airspace", n.cfg.Sources.Airspace},
		{"oref", n.cfg.Sources.Oref},
		{"ais", n.cfg.Sources.AIS},
		{"telegram", n.cfg.Sources.Telegram},
		{"xtracker", n.cfg.Sources.XTracker},
		{"polymarket", n.cfg.Sources.Polymarket},
		{"maritime", n.cfg.Sources.Maritime},
	}

	var sources []source.Source
	for _, e := range entries {
		if !e.cfg.Enabled {
			continue
		}

		if !source.Has(e.name) {
			n.logger.Warn("source not registered, skipping", "source", e.name)
			continue
		}

		src, err := source.Create(e.name)
		if err != nil {
			return nil, fmt.Errorf("create source %s: %w", e.name, err)
		}

		// Check required credentials
		for _, envName := range src.RequiredCredentials() {
			if os.Getenv(envName) == "" {
				n.logger.Warn("missing credential, skipping source", "source", e.name, "env", envName)
				continue
			}
		}

		if err := src.Init(e.cfg); err != nil {
			n.logger.Error("failed to init source, skipping", "source", e.name, "error", err)
			continue
		}

		sources = append(sources, src)
		n.logger.Info("source enabled", "source", e.name)
	}

	return sources, nil
}

// initNetwork sets up the network submission layer using the shared secret.
func (n *Node) initNetwork() error {
	if n.cfg.Node.ID == "" || n.cfg.Node.Secret == "" {
		return fmt.Errorf("node.id and node.secret must be set (get them from Platform → Nodes)")
	}

	n.nodeAuth = network.NewNodeAuth(
		n.cfg.Network.BackendURL,
		n.cfg.Node.ID,
		n.cfg.Node.Secret,
		n.logger,
	)

	n.submitter = network.NewSubmitter(n.nodeAuth, n.store, n.cfg.Network.BatchSize, n.logger)
	n.ctrlServer.SetCrowdsourceLog(n.submitter.CrowdsourceLog())

	n.heartbeat = network.NewHeartbeat(
		n.nodeAuth,
		func() []string {
			statuses := n.scheduler.Status()
			names := make([]string, len(statuses))
			for i, s := range statuses {
				names[i] = s.Name
			}
			return names
		},
		n.logger,
		n.metricsCollector,
	)
	n.heartbeat.SetVersion(n.version)

	return nil
}

// consumeData reads from the data channel and stores/broadcasts/queues results.
func (n *Node) consumeData(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case batch := <-n.dataCh:
			if err := n.store.InsertBatch(batch); err != nil {
				n.logger.Error("failed to store batch", "source", batch.Source, "error", err)
			}

			// Immediately stream signals to the backend crowdsource pipeline.
			// This runs concurrently and does not block — each signal is sent
			// as soon as it arrives, allowing the backend to deduplicate across
			// all nodes in real-time using embedding similarity.
			if n.submitter != nil && len(batch.Signals) > 0 {
				n.submitter.StreamSignals(batch.Signals)
			}

			if len(batch.Signals) > 0 {
				n.broadcaster.Send("signal", map[string]any{
					"signals": batch.Signals,
				})
			}
			if len(batch.Aircraft) > 0 {
				n.broadcaster.Send("aircraft", map[string]any{
					"positions": batch.Aircraft,
					"count":     len(batch.Aircraft),
					"timestamp": time.Now().UTC().Format(time.RFC3339),
				})
			}
			if len(batch.Vessels) > 0 {
				n.broadcaster.Send("vessel", map[string]any{
					"positions": batch.Vessels,
					"count":     len(batch.Vessels),
					"timestamp": time.Now().UTC().Format(time.RFC3339),
				})
			}
			if len(batch.Alerts) > 0 {
				n.broadcaster.Send("alert", map[string]any{
					"alerts": batch.Alerts,
					"count":  len(batch.Alerts),
				})
			}
			if len(batch.Markets) > 0 {
				n.broadcaster.Send("market", map[string]any{
					"markets": batch.Markets,
				})
			}
			if len(batch.Posts) > 0 {
				for _, p := range batch.Posts {
					if p.Platform == "twitter" {
						n.broadcaster.Send("x_post", map[string]any{"posts": []any{p}})
					} else {
						n.broadcaster.Send("tg_message", map[string]any{"messages": []any{p}})
					}
				}
				// Stream posts to the crowdsource pipeline as SOCIAL_MEDIA signals.
				if n.submitter != nil {
					n.submitter.StreamSignals(postsToSignals(batch.Posts))
				}
			}

			if batch.HasErrors() {
				n.logger.Warn("batch had errors", "source", batch.Source, "errors", batch.Errors)
			}
		}
	}
}

// runSubmissionLoop periodically submits pending data to the network.
func (n *Node) runSubmissionLoop(ctx context.Context) {
	ticker := time.NewTicker(n.cfg.Network.SubmissionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := n.submitter.SubmitPending(); err != nil {
				n.logger.Error("submission failed", "error", err)
			}
		}
	}
}

// runHeartbeatLoop periodically sends heartbeats to the backend.
func (n *Node) runHeartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(n.cfg.Network.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := n.heartbeat.Send(); err != nil {
				n.logger.Error("heartbeat failed", "error", err)
			}
		}
	}
}

// runPruneLoop periodically cleans up old data.
func (n *Node) runPruneLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := n.store.Prune(); err != nil {
				n.logger.Error("prune failed", "error", err)
			}
		}
	}
}

// restart performs a graceful restart of the node.
func (n *Node) restart() error {
	n.logger.Info("performing graceful restart")
	n.scheduler.Stop()

	sources, err := n.initSources()
	if err != nil {
		return fmt.Errorf("re-init sources: %w", err)
	}

	n.scheduler = scheduler.New(sources, n.dataCh, n.logger)
	n.scheduler.Start(context.Background())

	n.logger.Info("restart complete")
	return nil
}

// postsToSignals converts social media posts (Telegram/Twitter) to the
// NormalizedSignal format so they can flow through the crowdsource pipeline.
func postsToSignals(posts []source.NormalizedPost) []source.NormalizedSignal {
	sigs := make([]source.NormalizedSignal, 0, len(posts))
	for _, p := range posts {
		text := strings.TrimSpace(p.Text)
		if text == "" {
			continue
		}
		title := text
		if len(title) > 120 {
			// Trim at last space before 120 chars for a clean cut
			if idx := strings.LastIndex(title[:120], " "); idx > 40 {
				title = title[:idx]
			} else {
				title = title[:120]
			}
		}
		src := "TELEGRAM"
		if p.Platform == "twitter" {
			src = "TWITTER"
		}
		sigs = append(sigs, source.NormalizedSignal{
			Title:       title,
			Description: text,
			Severity:    "LOW",
			Category:    "SOCIAL_MEDIA",
			Source:      src,
			SourceRef:   p.URL,
			CollectedAt: p.Timestamp,
			Metadata: map[string]any{
				"author":   p.Author,
				"views":    p.Views,
				"platform": p.Platform,
			},
		})
	}
	return sigs
}
