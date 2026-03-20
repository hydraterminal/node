package usgs

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const apiURL = "https://earthquake.usgs.gov/earthquakes/feed/v1.0/summary/2.5_day.geojson"

func init() {
	source.Register("usgs", func() source.Source { return &Source{} })
}

// Source implements the USGS earthquake data source.
type Source struct {
	cfg    config.SourceConfig
	client *http.Client
}

func (s *Source) Name() string                       { return "usgs" }
func (s *Source) Type() source.SourceType            { return source.SourceTypePoll }
func (s *Source) Interval() time.Duration            { return s.cfg.Interval }
func (s *Source) RequiredCredentials() []string       { return nil }
func (s *Source) Stop() error                        { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	if s.cfg.Interval == 0 {
		s.cfg.Interval = 5 * time.Minute
	}
	s.client = &http.Client{Timeout: 15 * time.Second}
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch USGS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("USGS returned %d", resp.StatusCode)
	}

	var feed geoJSONFeed
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return fmt.Errorf("decode USGS: %w", err)
	}

	batch := source.CollectedBatch{
		Source:      "usgs",
		CollectedAt: time.Now(),
		Duration:    time.Since(start),
	}

	for _, f := range feed.Features {
		if len(f.Geometry.Coordinates) < 2 {
			continue
		}

		mag := f.Properties.Mag
		if mag < 2.5 {
			continue
		}

		lng := f.Geometry.Coordinates[0]
		lat := f.Geometry.Coordinates[1]

		severity := magnitudeToSeverity(mag)
		title := fmt.Sprintf("M%.1f Earthquake", mag)

		sig := source.NormalizedSignal{
			Title:       title,
			Description: f.Properties.Place,
			Severity:    severity,
			Category:    "EARTHQUAKE",
			Source:      "USGS",
			SourceRef:   fmt.Sprintf("usgs-%s", f.ID),
			Latitude:    &lat,
			Longitude:   &lng,
			CollectedAt: time.Now(),
			Metadata: map[string]any{
				"magnitude": mag,
				"place":     f.Properties.Place,
				"tsunami":   f.Properties.Tsunami,
				"sig":       f.Properties.Sig,
				"url":       f.Properties.URL,
				"alert":     f.Properties.Alert,
				"depth":     depthFromCoords(f.Geometry.Coordinates),
			},
		}

		batch.Signals = append(batch.Signals, sig)
	}

	out <- batch
	return nil
}

func magnitudeToSeverity(mag float64) string {
	switch {
	case mag >= 7.0:
		return "CRITICAL"
	case mag >= 6.0:
		return "HIGH"
	case mag >= 5.0:
		return "MEDIUM"
	case mag >= 4.0:
		return "LOW"
	default:
		return "INFO"
	}
}

func depthFromCoords(coords []float64) float64 {
	if len(coords) >= 3 {
		return math.Abs(coords[2])
	}
	return 0
}

// GeoJSON types

type geoJSONFeed struct {
	Features []feature `json:"features"`
}

type feature struct {
	ID       string   `json:"id"`
	Geometry geometry `json:"geometry"`
	Properties properties `json:"properties"`
}

type geometry struct {
	Coordinates []float64 `json:"coordinates"`
}

type properties struct {
	Mag     float64 `json:"mag"`
	Place   string  `json:"place"`
	Time    int64   `json:"time"`
	Alert   string  `json:"alert"`
	Tsunami int     `json:"tsunami"`
	Sig     int     `json:"sig"`
	URL     string  `json:"url"`
}
