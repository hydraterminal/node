package maritime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const maradURL = "https://www.maritime.dot.gov/msci-advisories"

func init() {
	source.Register("maritime", func() source.Source { return &Source{} })
}

type Source struct {
	cfg        config.SourceConfig
	client     *http.Client
	seen       map[string]time.Time
	lastSuccess time.Time
}

func (s *Source) Name() string                 { return "maritime" }
func (s *Source) Type() source.SourceType      { return source.SourceTypePoll }
func (s *Source) Interval() time.Duration      { return s.cfg.Interval }
func (s *Source) RequiredCredentials() []string { return nil }
func (s *Source) Stop() error                  { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	if s.cfg.Interval == 0 {
		s.cfg.Interval = 24 * time.Hour
	}
	s.client = &http.Client{Timeout: 30 * time.Second}
	s.seen = make(map[string]time.Time)
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	// Skip if recently polled successfully
	if !s.lastSuccess.IsZero() && time.Since(s.lastSuccess) < 23*time.Hour {
		return nil
	}

	start := time.Now()

	batch := source.CollectedBatch{
		Source:      "maritime",
		CollectedAt: time.Now(),
	}

	// Clean old dedup
	cutoff := time.Now().Add(-24 * time.Hour)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}

	// Fetch MARAD advisory page
	req, err := http.NewRequestWithContext(ctx, "GET", maradURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch MARAD: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MARAD returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Extract advisories from HTML using regex patterns
	// The TS version uses LLM extraction, here we do basic pattern matching
	zones := extractAdvisories(string(body))

	for _, zone := range zones {
		ref := fmt.Sprintf("marad-%s", zone.AdvisoryID)
		if _, dup := s.seen[ref]; dup {
			continue
		}
		s.seen[ref] = time.Now()

		lat := zone.CentroidLat
		lng := zone.CentroidLng

		sig := source.NormalizedSignal{
			Title:       fmt.Sprintf("Maritime Advisory: %s", zone.ZoneName),
			Description: zone.Summary,
			Severity:    zone.ThreatLevel,
			Category:    "NAVAL",
			Source:      "SYSTEM",
			SourceRef:   ref,
			Latitude:    &lat,
			Longitude:   &lng,
			CollectedAt: time.Now(),
			Metadata: map[string]any{
				"provider":    "marad",
				"zoneType":    zone.ZoneType,
				"zoneName":    zone.ZoneName,
				"advisoryId":  zone.AdvisoryID,
				"threatLevel": zone.ThreatLevel,
				"validUntil":  zone.ValidUntil,
				"url":         maradURL,
			},
		}
		batch.Signals = append(batch.Signals, sig)
	}

	batch.Duration = time.Since(start)
	out <- batch
	s.lastSuccess = time.Now()
	return nil
}

type maritimeZone struct {
	ZoneName    string  `json:"zoneName"`
	ZoneType    string  `json:"zoneType"`
	Region      string  `json:"region"`
	CentroidLat float64 `json:"centroidLat"`
	CentroidLng float64 `json:"centroidLng"`
	ThreatLevel string  `json:"threatLevel"`
	Summary     string  `json:"summary"`
	AdvisoryID  string  `json:"advisoryId"`
	ValidUntil  string  `json:"validUntil"`
}

var (
	advisoryIDRe = regexp.MustCompile(`(?i)(?:MSCI|Advisory)\s*(?:#|No\.?|Number)?\s*(\d{4}-\d{1,4}[A-Za-z]?)`)
	regionRe     = regexp.MustCompile(`(?i)(Red Sea|Gulf of Aden|Persian Gulf|Arabian Gulf|Strait of Hormuz|Gulf of Oman|Black Sea|Mediterranean|South China Sea|Indian Ocean|Baltic Sea)`)
)

// extractAdvisories does basic HTML pattern matching for maritime advisories.
// The TS version uses LLM for richer extraction; this is a simpler fallback.
func extractAdvisories(html string) []maritimeZone {
	// Strip HTML tags
	htmlTagRe := regexp.MustCompile(`<[^>]+>`)
	text := htmlTagRe.ReplaceAllString(html, " ")
	text = strings.Join(strings.Fields(text), " ")

	var zones []maritimeZone

	// Find advisory IDs and their surrounding context
	matches := advisoryIDRe.FindAllStringSubmatchIndex(text, 10)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		advID := text[match[2]:match[3]]

		// Get surrounding context (500 chars around the match)
		ctxStart := match[0] - 250
		if ctxStart < 0 {
			ctxStart = 0
		}
		ctxEnd := match[1] + 250
		if ctxEnd > len(text) {
			ctxEnd = len(text)
		}
		context := text[ctxStart:ctxEnd]

		// Try to find region
		region := "Unknown"
		if regionMatch := regionRe.FindString(context); regionMatch != "" {
			region = regionMatch
		}

		// Map known regions to approximate coordinates
		lat, lng := regionToCoords(region)

		zone := maritimeZone{
			ZoneName:    region,
			ZoneType:    "EXCLUSION",
			Region:      region,
			CentroidLat: lat,
			CentroidLng: lng,
			ThreatLevel: "HIGH",
			Summary:     truncate(context, 200),
			AdvisoryID:  advID,
		}
		zones = append(zones, zone)
	}

	// Deduplicate by advisory ID
	seen := make(map[string]bool)
	var deduped []maritimeZone
	for _, z := range zones {
		if !seen[z.AdvisoryID] {
			seen[z.AdvisoryID] = true
			deduped = append(deduped, z)
		}
	}

	if len(deduped) > 5 {
		deduped = deduped[:5]
	}
	return deduped
}

func regionToCoords(region string) (float64, float64) {
	coords := map[string][2]float64{
		"Red Sea":            {20.0, 38.0},
		"Gulf of Aden":       {12.5, 47.0},
		"Persian Gulf":       {26.0, 52.0},
		"Arabian Gulf":       {26.0, 52.0},
		"Strait of Hormuz":   {26.5, 56.3},
		"Gulf of Oman":       {24.5, 58.0},
		"Black Sea":          {43.0, 34.0},
		"Mediterranean":      {35.0, 18.0},
		"South China Sea":    {12.0, 114.0},
		"Indian Ocean":       {-5.0, 70.0},
		"Baltic Sea":         {58.0, 20.0},
	}
	if c, ok := coords[region]; ok {
		return c[0], c[1]
	}
	return 0, 0
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// LLM extraction could be added here as an enhancement.
// The Go node would call an LLM API endpoint (OpenRouter) to extract
// structured zone data from the raw advisory text.
// For now, the regex-based extraction provides basic functionality.
func extractWithLLM(text string, apiKey string) ([]maritimeZone, error) {
	// Placeholder for LLM-based extraction
	// This would POST to OpenRouter with the extraction prompt
	// and parse the JSON response into maritimeZone structs.
	prompt := fmt.Sprintf(`Extract maritime security zones from this text. Return JSON array with: zoneName, zoneType (EXCLUSION/PIRACY/TSA/OTHER), region, centroidLat, centroidLng, threatLevel (CRITICAL/HIGH/MEDIUM/LOW/INFO), summary, advisoryId, validUntil. Limit to 5 zones.

Text: %s`, truncate(text, 4000))

	_ = prompt
	return nil, fmt.Errorf("LLM extraction not yet implemented")
}
