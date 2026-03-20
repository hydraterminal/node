package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hydraterminal/node/internal/metrics"
)

// Heartbeat sends periodic heartbeats to the Hydra backend.
type Heartbeat struct {
	auth          *NodeAuth
	activeSources func() []string
	logger        *slog.Logger
	startTime     time.Time
	signalsCount  int
	metrics       *metrics.Collector
}

// NewHeartbeat creates a new heartbeat sender.
func NewHeartbeat(auth *NodeAuth, activeSources func() []string, logger *slog.Logger, mc *metrics.Collector) *Heartbeat {
	return &Heartbeat{
		auth:          auth,
		activeSources: activeSources,
		logger:        logger,
		startTime:     time.Now(),
		metrics:       mc,
	}
}

// IncrSignals increments the signal submission counter.
func (h *Heartbeat) IncrSignals(n int) {
	h.signalsCount += n
}

// Send sends a single heartbeat to the backend.
func (h *Heartbeat) Send() error {
	snap := h.metrics.Latest()

	payload := HeartbeatPayload{
		NodeID:        h.auth.NodeID(),
		Timestamp:     time.Now().UTC(),
		Status:        "healthy",
		ActiveSources: h.activeSources(),
		Metrics: HeartbeatMetrics{
			SignalsSubmitted: h.signalsCount,
			UptimeSeconds:    int64(time.Since(h.startTime).Seconds()),
			MemoryMB:         snap.MemoryMB,
			CPUPercent:       snap.CPUPercent,
			NetworkTxBytes:   snap.NetworkTxBytes,
			NetworkRxBytes:   snap.NetworkRxBytes,
		},
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/nodes/heartbeat", h.auth.BackendURL())

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	resp, err := h.auth.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat returned %d", resp.StatusCode)
	}

	return nil
}
