package control

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/metrics"
	"github.com/hydraterminal/node/internal/network"
	"github.com/hydraterminal/node/internal/scheduler"
	"github.com/hydraterminal/node/internal/store"
	"gopkg.in/yaml.v3"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Server provides control plane endpoints (restart, logs, status).
// These are mounted on the main API server under /control/* and
// protected by the node's shared secret.
type Server struct {
	scheduler  *scheduler.Scheduler
	logger     *slog.Logger
	restartFn  func() error
	logBuffer  *LogBuffer
	startTime  time.Time
	secret     string // shared secret for auth
	store      *store.SQLiteStore
	metrics    *metrics.Collector
	cfg        *config.Config
	configPath string
	version    string
	csLog      *network.CrowdsourceLog
}

// SetCrowdsourceLog attaches the crowdsource submission log so the control
// plane can serve it via GET /control/crowdsource-log.
func (s *Server) SetCrowdsourceLog(log *network.CrowdsourceLog) { s.csLog = log }

// SetVersion sets the version string reported in status and metrics.
func (s *Server) SetVersion(v string) { s.version = v }

// NewServer creates a control plane handler with its own log buffer.
func NewServer(sched *scheduler.Scheduler, logger *slog.Logger, restartFn func() error, secret string) *Server {
	return NewServerWithBuffer(sched, logger, restartFn, secret, NewLogBuffer(1000), nil, nil, nil, "")
}

// NewServerWithBuffer creates a control plane handler with an externally-provided log buffer.
// This allows the node's slog handler to tee into the same buffer that feeds the log stream.
func NewServerWithBuffer(sched *scheduler.Scheduler, logger *slog.Logger, restartFn func() error, secret string, buf *LogBuffer, st *store.SQLiteStore, mc *metrics.Collector, cfg *config.Config, configPath string) *Server {
	return &Server{
		scheduler:  sched,
		logger:     logger,
		restartFn:  restartFn,
		logBuffer:  buf,
		startTime:  time.Now(),
		secret:     secret,
		store:      st,
		metrics:    mc,
		cfg:        cfg,
		configPath: configPath,
	}
}

// Register mounts control routes onto the given ServeMux.
// All routes are under /control/* and require the node secret.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /control/restart", s.requireSecret(s.handleRestart))
	mux.HandleFunc("POST /control/restart-source/{name}", s.requireSecret(s.handleRestartSource))
	mux.HandleFunc("GET /control/logs", s.requireSecret(s.handleLogs))
	mux.HandleFunc("GET /control/status", s.requireSecret(s.handleStatus))
	mux.HandleFunc("POST /control/source/{name}/enable", s.requireSecret(s.handleEnableSource))
	mux.HandleFunc("POST /control/source/{name}/disable", s.requireSecret(s.handleDisableSource))
	mux.HandleFunc("GET /control/metrics", s.requireSecret(s.handleMetrics))
	mux.HandleFunc("GET /control/api-keys", s.requireSecret(s.handleListApiKeys))
	mux.HandleFunc("POST /control/api-keys", s.requireSecret(s.handleCreateApiKey))
	mux.HandleFunc("DELETE /control/api-keys/{id}", s.requireSecret(s.handleRevokeApiKey))
	mux.HandleFunc("GET /control/config", s.requireSecret(s.handleGetConfig))
	mux.HandleFunc("PUT /control/config", s.requireSecret(s.handlePutConfig))
	mux.HandleFunc("PATCH /control/config", s.requireSecret(s.handlePatchConfig))
	mux.HandleFunc("GET /control/crowdsource-log", s.requireSecret(s.handleCrowdsourceLog))
	mux.HandleFunc("/control/logs/stream", s.HandleLogStream)
}

// LogBuffer returns the log buffer for writing logs to.
func (s *Server) LogBuffer() *LogBuffer {
	return s.logBuffer
}

// requireSecret checks that the request is authorized via one of:
// 1. Raw secret in x-node-secret header (for local/operator use)
// 2. HMAC signature in x-control-hmac header (for backend proxy)
func (s *Server) requireSecret(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.secret == "" {
			// No secret configured — allow all (private node, no auth)
			next(w, r)
			return
		}

		// Method 1: raw secret
		key := r.Header.Get("x-node-secret")
		if key == "" {
			key = r.URL.Query().Get("secret")
		}
		if key == s.secret {
			next(w, r)
			return
		}

		// Method 2: HMAC from backend proxy
		// Backend signs with HMAC-SHA256(secretKeyHash, "timestamp:action")
		// Node computes secretKeyHash = SHA256(secret) and verifies
		hmacHeader := r.Header.Get("x-control-hmac")
		tsHeader := r.Header.Get("x-control-timestamp")
		if hmacHeader != "" && tsHeader != "" {
			ts, err := strconv.ParseInt(tsHeader, 10, 64)
			if err == nil && math.Abs(float64(time.Now().UnixMilli()-ts)) < 60_000 {
				// Extract action from path: /control/{action}
				parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/control/"), "/")
				action := parts[0]

				// Compute secretKeyHash = SHA256(secret)
				secretHash := sha256.Sum256([]byte(s.secret))
				secretHashHex := hex.EncodeToString(secretHash[:])

				// Verify HMAC
				mac := hmac.New(sha256.New, []byte(secretHashHex))
				mac.Write([]byte(fmt.Sprintf("%s:%s", tsHeader, action)))
				expected := hex.EncodeToString(mac.Sum(nil))

				if hmac.Equal([]byte(hmacHeader), []byte(expected)) {
					next(w, r)
					return
				}
			}
		}

		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	s.logger.Info("restart requested via control plane")

	if s.restartFn == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "restart not configured"})
		return
	}

	go func() {
		if err := s.restartFn(); err != nil {
			s.logger.Error("restart failed", "error", err)
		}
	}()

	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

func (s *Server) handleRestartSource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source name required"})
		return
	}

	s.logger.Info("source restart requested", "source", name)
	if err := s.scheduler.RestartSource(context.Background(), name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted", "source": name})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	source := r.URL.Query().Get("source")

	logs := s.logBuffer.Get(level, source, 200)
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	resp := map[string]any{
		"status":    "running",
		"version":   s.version,
		"uptime":    time.Since(s.startTime).String(),
		"startedAt": s.startTime.UTC().Format(time.RFC3339),
		"sources":   s.scheduler.Status(),
		"memory": map[string]any{
			"allocMB": mem.Alloc / 1024 / 1024,
			"sysMB":   mem.Sys / 1024 / 1024,
		},
		"goroutines": runtime.NumGoroutine(),
	}

	if s.metrics != nil {
		snap := s.metrics.Latest()
		resp["cpuPercent"] = snap.CPUPercent
		resp["networkTxBytes"] = snap.NetworkTxBytes
		resp["networkRxBytes"] = snap.NetworkRxBytes
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleEnableSource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	writeJSON(w, http.StatusOK, map[string]string{"status": "enabled", "source": name})
}

func (s *Server) handleDisableSource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled", "source": name})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "# HELP hydra_node_uptime_seconds Node uptime in seconds\n")
	fmt.Fprintf(w, "hydra_node_uptime_seconds %.0f\n", time.Since(s.startTime).Seconds())
	fmt.Fprintf(w, "# HELP hydra_node_memory_alloc_bytes Allocated memory in bytes\n")
	fmt.Fprintf(w, "hydra_node_memory_alloc_bytes %d\n", mem.Alloc)
	fmt.Fprintf(w, "# HELP hydra_node_goroutines Number of goroutines\n")
	fmt.Fprintf(w, "hydra_node_goroutines %d\n", runtime.NumGoroutine())
}

// HandleLogStream serves the /control/logs/stream WebSocket endpoint.
// Auth accepts:
//   1. x-node-secret header or ?secret= query param (raw secret)
//   2. ?token= HMAC token with ?ts= timestamp: HMAC-SHA256(SHA256(secret), "logs:"+ts), valid 5 min
func (s *Server) HandleLogStream(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeLogStream(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("log stream ws upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	s.logger.Info("log stream client connected", "remote", r.RemoteAddr)

	// Send last 100 log entries as history.
	history := s.logBuffer.Get("", "", 500)
	historyMsg, _ := json.Marshal(map[string]any{
		"type": "log_history",
		"data": map[string]any{"logs": history},
	})
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, historyMsg); err != nil {
		s.logger.Error("log stream: failed to send history", "error", err)
		return
	}

	// Subscribe to new log entries.
	logCh := make(chan LogEntry, 256)
	s.logBuffer.Subscribe(logCh)
	defer s.logBuffer.Unsubscribe(logCh)

	// Read pump — keeps connection alive via pong handling.
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn.SetReadLimit(512)
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			return nil
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Send initial metrics snapshot immediately so the UI doesn't wait.
	if s.metrics != nil {
		snap := s.metrics.Latest()
		metricsMsg, _ := json.Marshal(map[string]any{
			"type": "metrics",
			"data": map[string]any{
				"cpuPercent":     snap.CPUPercent,
				"memoryMB":       snap.MemoryMB,
				"networkTxBytes": snap.NetworkTxBytes,
				"networkRxBytes": snap.NetworkRxBytes,
				"uptimeSeconds":  int64(time.Since(s.startTime).Seconds()),
				"goroutines":     snap.Goroutines,
				"version":        s.version,
			},
		})
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = conn.WriteMessage(websocket.TextMessage, metricsMsg)
	}

	// Write pump — streams logs + metrics + sends pings.
	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()

	metricsTicker := time.NewTicker(3 * time.Second)
	defer metricsTicker.Stop()

	for {
		select {
		case entry, ok := <-logCh:
			if !ok {
				return
			}
			msg, _ := json.Marshal(map[string]any{
				"type": "log",
				"data": entry,
			})
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-metricsTicker.C:
			if s.metrics == nil {
				continue
			}
			snap := s.metrics.Latest()
			msg, _ := json.Marshal(map[string]any{
				"type": "metrics",
				"data": map[string]any{
					"cpuPercent":     snap.CPUPercent,
					"memoryMB":       snap.MemoryMB,
					"networkTxBytes": snap.NetworkTxBytes,
					"networkRxBytes": snap.NetworkRxBytes,
					"uptimeSeconds":  int64(time.Since(s.startTime).Seconds()),
					"goroutines":     snap.Goroutines,
				},
			})
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-pingTicker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-done:
			return
		}
	}
}

// authorizeLogStream checks auth for the log stream WebSocket endpoint.
func (s *Server) authorizeLogStream(r *http.Request) bool {
	// No secret configured — allow all (private node).
	if s.secret == "" {
		return true
	}

	// Method 1: raw secret via header or query param.
	key := r.Header.Get("x-node-secret")
	if key == "" {
		key = r.URL.Query().Get("secret")
	}
	if key == s.secret {
		return true
	}

	// Method 2: HMAC token — HMAC-SHA256(SHA256(secret), "logs:"+ts), valid 5 min.
	token := r.URL.Query().Get("token")
	tsStr := r.URL.Query().Get("ts")
	if token != "" && tsStr != "" {
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			return false
		}
		// Check timestamp within 5 minute window.
		if math.Abs(float64(time.Now().UnixMilli()-ts)) > 5*60*1000 {
			return false
		}

		secretHash := sha256.Sum256([]byte(s.secret))
		secretHashHex := hex.EncodeToString(secretHash[:])

		mac := hmac.New(sha256.New, []byte(secretHashHex))
		mac.Write([]byte(fmt.Sprintf("logs:%s", tsStr)))
		expected := hex.EncodeToString(mac.Sum(nil))

		if hmac.Equal([]byte(token), []byte(expected)) {
			return true
		}
	}

	return false
}

func (s *Server) handleListApiKeys(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not available"})
		return
	}
	keys, err := s.store.ListApiKeys()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if keys == nil {
		keys = []store.ApiKeyInfo{}
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateApiKey(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not available"})
		return
	}

	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if len(body.Scopes) == 0 {
		body.Scopes = []string{"read"}
	}

	// Generate a random key: hyd_ + 32 random hex bytes (64 chars)
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate key"})
		return
	}
	fullKey := "hyd_" + hex.EncodeToString(randomBytes)
	keyPrefix := fullKey[:12] // "hyd_" + first 8 hex chars

	// Hash the full key with SHA-256 for storage
	hash := sha256.Sum256([]byte(fullKey))
	keyHash := hex.EncodeToString(hash[:])

	id := uuid.New().String()
	if err := s.store.CreateApiKey(id, body.Name, keyPrefix, keyHash, body.Scopes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	s.logger.Info("API key created", "id", id, "name", body.Name, "prefix", keyPrefix)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        id,
		"name":      body.Name,
		"key":       fullKey,
		"keyPrefix": keyPrefix,
		"scopes":    body.Scopes,
	})
}

func (s *Server) handleRevokeApiKey(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not available"})
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}

	if err := s.store.RevokeApiKey(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	s.logger.Info("API key revoked", "id", id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked", "id": id})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config not available"})
		return
	}

	format := r.URL.Query().Get("format")
	if format == "raw" {
		raw, err := config.ReadRaw(s.configPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"yaml": raw})
		return
	}

	// Return structured config — secret is yaml:"-" so it never appears in YAML,
	// and we explicitly zero it for the JSON response.
	safeCfg := *s.cfg
	safeCfg.Node.Secret = ""

	out, err := yaml.Marshal(&safeCfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"config": &safeCfg,
		"yaml":   string(out),
	})
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config not available"})
		return
	}

	var body struct {
		YAML    string `json:"yaml"`
		Restart bool   `json:"restart"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if body.YAML == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "yaml field is required"})
		return
	}

	// Validate & parse
	var newCfg config.Config
	if err := yaml.Unmarshal([]byte(body.YAML), &newCfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid YAML: " + err.Error()})
		return
	}

	// Write to disk
	if err := config.SaveRaw(body.YAML, s.configPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save: " + err.Error()})
		return
	}

	// Preserve the env-sourced secret (never in YAML)
	newCfg.Node.Secret = s.cfg.Node.Secret
	*s.cfg = newCfg

	s.logger.Info("config updated via control plane", "restart", body.Restart)

	if body.Restart && s.restartFn != nil {
		go func() {
			if err := s.restartFn(); err != nil {
				s.logger.Error("restart after config update failed", "error", err)
			}
		}()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// jsonSourceConfig is a JSON-friendly version of config.SourceConfig
// where durations are strings like "5m0s" instead of nanosecond ints.
type jsonSourceConfig struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	Interval       string   `json:"interval,omitempty"`
	FlushInterval  string   `json:"flushInterval,omitempty"`
	APIKeyEnv      *string  `json:"apiKeyEnv,omitempty"`
	APIHostEnv     *string  `json:"apiHostEnv,omitempty"`
	OpenaipKeyEnv  *string  `json:"openaipKeyEnv,omitempty"`
	ProxyURL       *string  `json:"proxyUrl,omitempty"`
	Accounts       []string `json:"accounts,omitempty"`
	Cookies        []string `json:"cookies,omitempty"`
	Channels       []string `json:"channels,omitempty"`
	MarketInterval string   `json:"marketInterval,omitempty"`
}

// applyTo merges non-nil/non-empty fields into a live SourceConfig.
func (j *jsonSourceConfig) applyTo(target *config.SourceConfig) error {
	if j.Enabled != nil {
		target.Enabled = *j.Enabled
	}
	if j.Interval != "" {
		d, err := time.ParseDuration(j.Interval)
		if err != nil {
			return fmt.Errorf("invalid interval %q: %w", j.Interval, err)
		}
		target.Interval = d
	}
	if j.FlushInterval != "" {
		d, err := time.ParseDuration(j.FlushInterval)
		if err != nil {
			return fmt.Errorf("invalid flushInterval %q: %w", j.FlushInterval, err)
		}
		target.FlushInterval = d
	}
	if j.MarketInterval != "" {
		d, err := time.ParseDuration(j.MarketInterval)
		if err != nil {
			return fmt.Errorf("invalid marketInterval %q: %w", j.MarketInterval, err)
		}
		target.MarketInterval = d
	}
	if j.APIKeyEnv != nil {
		target.APIKeyEnv = *j.APIKeyEnv
	}
	if j.APIHostEnv != nil {
		target.APIHostEnv = *j.APIHostEnv
	}
	if j.OpenaipKeyEnv != nil {
		target.OpenaipKeyEnv = *j.OpenaipKeyEnv
	}
	if j.ProxyURL != nil {
		target.ProxyURL = *j.ProxyURL
	}
	if j.Accounts != nil {
		target.Accounts = j.Accounts
	}
	if j.Cookies != nil {
		target.Cookies = j.Cookies
	}
	if j.Channels != nil {
		target.Channels = j.Channels
	}
	return nil
}

// handlePatchConfig merges a partial sources update into the existing config.
// This avoids fragile YAML splicing — the frontend sends structured JSON.
func (s *Server) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	if s.cfg == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config not available"})
		return
	}

	var body struct {
		Sources map[string]jsonSourceConfig `json:"sources"`
		Restart bool                        `json:"restart"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if len(body.Sources) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sources field is required"})
		return
	}

	// Map source name → pointer to SourceConfig in the live config
	sourceMap := map[string]*config.SourceConfig{
		"usgs":       &s.cfg.Sources.USGS,
		"adsb":       &s.cfg.Sources.ADSB,
		"ais":        &s.cfg.Sources.AIS,
		"oref":       &s.cfg.Sources.Oref,
		"cyber":      &s.cfg.Sources.Cyber,
		"airspace":   &s.cfg.Sources.Airspace,
		"xtracker":   &s.cfg.Sources.XTracker,
		"telegram":   &s.cfg.Sources.Telegram,
		"polymarket": &s.cfg.Sources.Polymarket,
		"maritime":   &s.cfg.Sources.Maritime,
	}

	for name, patch := range body.Sources {
		target, ok := sourceMap[name]
		if !ok {
			s.logger.Warn("unknown source in patch", "source", name)
			continue
		}
		if err := patch.applyTo(target); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("source %s: %s", name, err.Error())})
			return
		}
	}

	// Persist to disk
	if err := config.Save(s.cfg, s.configPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save: " + err.Error()})
		return
	}

	s.logger.Info("config patched via control plane", "sources", len(body.Sources), "restart", body.Restart)

	if body.Restart && s.restartFn != nil {
		go func() {
			if err := s.restartFn(); err != nil {
				s.logger.Error("restart after config patch failed", "error", err)
			}
		}()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleCrowdsourceLog(w http.ResponseWriter, r *http.Request) {
	if s.csLog == nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}, "stats": map[string]int{}})
		return
	}
	entries := s.csLog.Entries()
	total, newCount, confirmed, errors := s.csLog.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"stats": map[string]any{
			"total":     total,
			"new":       newCount,
			"confirmed": confirmed,
			"errors":    errors,
		},
	})
}
