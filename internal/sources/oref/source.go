package oref

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const (
	wsURL = "wss://ws.tzevaadom.co.il/socket?platform=WEB"
	alertExpiryDuration = 5 * time.Minute
)

func init() {
	source.Register("oref", func() source.Source { return &Source{} })
}

type Source struct {
	cfg    config.SourceConfig
	conn   *websocket.Conn
	logger *slog.Logger
	seen   map[string]time.Time
}

func (s *Source) Name() string                 { return "oref" }
func (s *Source) Type() source.SourceType      { return source.SourceTypeStream }
func (s *Source) Interval() time.Duration      { return 0 }
func (s *Source) RequiredCredentials() []string { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	s.logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	s.seen = make(map[string]time.Time)
	return nil
}

func (s *Source) Stop() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	headers := map[string][]string{
		"User-Agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"},
		"Origin":          {"https://www.tzevaadom.co.il"},
		"Accept-Language": {"en-GB,en-US;q=0.9,en;q=0.8"},
		"Cache-Control":   {"no-cache"},
		"Pragma":          {"no-cache"},
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return fmt.Errorf("connect oref WS: %w", err)
	}
	s.conn = conn

	s.logger.Info("connected to Oref WebSocket")

	// Read messages
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read oref WS: %w", err)
		}

		var msg orefMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "ALERT":
			batch := s.processAlert(msg.Data)
			if batch != nil {
				out <- *batch
			}
		}
	}
}

func (s *Source) processAlert(data json.RawMessage) *source.CollectedBatch {
	var alert orefAlertData
	if err := json.Unmarshal(data, &alert); err != nil {
		return nil
	}

	if alert.IsDrill {
		return nil
	}

	ref := alert.NotificationID
	if ref == "" {
		ref = fmt.Sprintf("oref-%d", alert.Time)
	}

	// Dedup
	if _, dup := s.seen[ref]; dup {
		return nil
	}
	s.seen[ref] = time.Now()

	// Clean old entries
	cutoff := time.Now().Add(-1 * time.Hour)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}

	alertType := threatToAlertType(alert.Threat)
	severity := alertTypeSeverity(alertType)
	expiry := time.Now().Add(alertExpiryDuration)

	batch := &source.CollectedBatch{
		Source:      "oref",
		CollectedAt: time.Now(),
	}

	// Create alert entry
	normalAlert := source.NormalizedAlert{
		ID:          ref,
		AlertType:   alertType,
		Title:       fmt.Sprintf("%s Alert", alertType),
		Description: fmt.Sprintf("%s in %d areas", alertType, len(alert.Cities)),
		Severity:    severity,
		Cities:      alert.Cities,
		ExpiresAt:   expiry,
	}

	// TODO: geocode cities to get lat/lng centroid
	// For now use approximate Israel center
	normalAlert.Latitude = 31.5
	normalAlert.Longitude = 34.8

	batch.Alerts = append(batch.Alerts, normalAlert)

	// Also create a signal
	lat := normalAlert.Latitude
	lng := normalAlert.Longitude
	batch.Signals = append(batch.Signals, source.NormalizedSignal{
		Title:       normalAlert.Title,
		Description: normalAlert.Description,
		Severity:    severity,
		Category:    "MISSILE_ALERT",
		Source:      "OREF",
		SourceRef:   ref,
		Region:      "IL",
		Countries:   []string{"IL"},
		Latitude:    &lat,
		Longitude:   &lng,
		ExpiresAt:   &expiry,
		CollectedAt: time.Now(),
		Metadata: map[string]any{
			"alertType": alertType,
			"cities":    alert.Cities,
			"threat":    alert.Threat,
		},
	})

	return batch
}

func threatToAlertType(threat int) string {
	switch threat {
	case 0:
		return "MISSILES"
	case 1, 5:
		return "HOSTILE_AIRCRAFT"
	case 2:
		return "EARTHQUAKE"
	case 3:
		return "TSUNAMI"
	case 4:
		return "RADIOLOGICAL"
	case 6:
		return "TERRORIST_INFILTRATION"
	case 7:
		return "HAZARDOUS_MATERIALS"
	case 13:
		return "GENERAL"
	default:
		return "UNKNOWN"
	}
}

func alertTypeSeverity(alertType string) string {
	switch alertType {
	case "MISSILES", "HOSTILE_AIRCRAFT", "TERRORIST_INFILTRATION":
		return "CRITICAL"
	case "EARTHQUAKE", "TSUNAMI", "RADIOLOGICAL":
		return "HIGH"
	default:
		return "MEDIUM"
	}
}

// WebSocket message types

type orefMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type orefAlertData struct {
	NotificationID string   `json:"notificationId"`
	Time           int64    `json:"time"`
	Threat         int      `json:"threat"`
	IsDrill        bool     `json:"isDrill"`
	Cities         []string `json:"cities"`
}
