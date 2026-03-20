package cyber

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const (
	otxBaseURL = "https://otx.alienvault.com/api/v1"
	maxIndicatorsForCountry = 30
	maxSourceCountries      = 5
)

func init() {
	source.Register("cyber", func() source.Source { return &Source{} })
}

type Source struct {
	cfg    config.SourceConfig
	apiKey string
	client *http.Client
	seen   map[string]time.Time // sourceRef dedup
}

func (s *Source) Name() string                 { return "cyber" }
func (s *Source) Type() source.SourceType      { return source.SourceTypePoll }
func (s *Source) Interval() time.Duration      { return s.cfg.Interval }
func (s *Source) RequiredCredentials() []string { return []string{"OTX_API_KEY"} }
func (s *Source) Stop() error                  { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	if s.cfg.Interval == 0 {
		s.cfg.Interval = 5 * time.Minute
	}
	s.apiKey = config.GetSourceAPIKey(cfg.APIKeyEnv)
	if s.apiKey == "" {
		s.apiKey = config.GetSourceAPIKey("OTX_API_KEY")
	}
	s.client = &http.Client{Timeout: 30 * time.Second}
	s.seen = make(map[string]time.Time)
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	start := time.Now()

	url := fmt.Sprintf("%s/pulses/activity?limit=20&page=1", otxBaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-OTX-API-KEY", s.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch OTX: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OTX returned %d", resp.StatusCode)
	}

	var result otxResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode OTX: %w", err)
	}

	batch := source.CollectedBatch{
		Source:      "cyber",
		CollectedAt: time.Now(),
		Duration:    time.Since(start),
	}

	// Clean old dedup entries
	cutoff := time.Now().Add(-2 * time.Hour)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}

	for _, pulse := range result.Results {
		ref := fmt.Sprintf("otx-%s", pulse.ID)
		if _, dup := s.seen[ref]; dup {
			continue
		}
		s.seen[ref] = time.Now()

		severity := deriveSeverity(pulse)
		attackType := deriveAttackType(pulse)
		sourceCountries := extractSourceCountries(ctx, s.client, s.apiKey, pulse)

		sig := source.NormalizedSignal{
			Title:       pulse.Name,
			Description: truncate(pulse.Description, 500),
			Severity:    severity,
			Category:    "CYBER",
			Source:      "SYSTEM",
			SourceRef:   ref,
			Countries:   pulse.TargetedCountries,
			CollectedAt: time.Now(),
			Metadata: map[string]any{
				"provider":         "otx",
				"pulseId":          pulse.ID,
				"sourceCountries":  sourceCountries,
				"targetCountries":  pulse.TargetedCountries,
				"attackType":       attackType,
				"malwareFamilies":  extractMalwareFamilies(pulse),
				"indicatorCount":   pulse.IndicatorCount,
				"tlp":              pulse.TLP,
				"references":       pulse.References,
				"authorName":       pulse.AuthorName,
			},
		}

		batch.Signals = append(batch.Signals, sig)
	}

	out <- batch
	return nil
}

func deriveSeverity(p otxPulse) string {
	if p.TLP == "red" || p.IndicatorCount > 500 {
		return "CRITICAL"
	}
	if p.IndicatorCount > 100 {
		return "HIGH"
	}
	if p.IndicatorCount > 20 {
		return "MEDIUM"
	}
	if p.IndicatorCount > 5 {
		return "LOW"
	}
	return "INFO"
}

func deriveAttackType(p otxPulse) string {
	// Combine tags + malware family names for keyword search
	var terms []string
	for _, t := range p.Tags {
		terms = append(terms, strings.ToLower(t))
	}
	for _, m := range p.MalwareFamilies {
		terms = append(terms, strings.ToLower(m.DisplayName))
	}
	combined := strings.Join(terms, " ")

	switch {
	case strings.Contains(combined, "ransomware"):
		return "RANSOMWARE"
	case strings.Contains(combined, "botnet"):
		return "BOTNET"
	case strings.Contains(combined, "phishing") || strings.Contains(combined, "credential"):
		return "PHISHING"
	case strings.Contains(combined, "apt") || strings.Contains(combined, "espionage"):
		return "ESPIONAGE"
	case strings.Contains(combined, "exploit") || strings.Contains(combined, "vulnerability"):
		return "EXPLOIT"
	case strings.Contains(combined, "ddos"):
		return "DDOS"
	case strings.Contains(combined, "malware") || strings.Contains(combined, "trojan") || strings.Contains(combined, "backdoor"):
		return "MALWARE"
	default:
		return "OTHER"
	}
}

func extractSourceCountries(ctx context.Context, client *http.Client, apiKey string, pulse otxPulse) []string {
	url := fmt.Sprintf("%s/pulses/%s", otxBaseURL, pulse.ID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("X-OTX-API-KEY", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var detail otxPulseDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil
	}

	countrySet := make(map[string]bool)
	for i, ind := range detail.Indicators {
		if i >= maxIndicatorsForCountry {
			break
		}
		if ind.CountryCode != "" {
			countrySet[ind.CountryCode] = true
		}
		if len(countrySet) >= maxSourceCountries {
			break
		}
	}

	countries := make([]string, 0, len(countrySet))
	for cc := range countrySet {
		countries = append(countries, cc)
	}
	return countries
}

func extractMalwareFamilies(p otxPulse) []string {
	families := make([]string, len(p.MalwareFamilies))
	for i, m := range p.MalwareFamilies {
		families[i] = m.DisplayName
	}
	return families
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// OTX API types

type otxResponse struct {
	Results []otxPulse `json:"results"`
}

type otxPulse struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	AuthorName        string          `json:"author_name"`
	Tags              []string        `json:"tags"`
	TargetedCountries []string        `json:"targeted_countries"`
	MalwareFamilies   []malwareFamily `json:"malware_families"`
	AttackIDs         []attackID      `json:"attack_ids"`
	TLP               string          `json:"tlp"`
	IndicatorCount    int             `json:"indicator_count"`
	References        []string        `json:"references"`
}

type malwareFamily struct {
	DisplayName string `json:"display_name"`
}

type attackID struct {
	DisplayName string `json:"display_name"`
}

type otxPulseDetail struct {
	Indicators []otxIndicator `json:"indicators"`
}

type otxIndicator struct {
	Type        string `json:"type"`
	Indicator   string `json:"indicator"`
	CountryCode string `json:"country_code"`
}
