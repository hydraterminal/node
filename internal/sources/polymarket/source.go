package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const (
	analyticsURL = "https://polymarketanalytics.com/api/combined-markets"
	gammaURL     = "https://gamma-api.polymarket.com/markets"
	maxPages     = 8
	pageDelay    = 300 * time.Millisecond
)

var searchQueries = []string{
	"politics", "war", "military", "sanctions", "nuclear", "election",
	"geopolitics", "conflict", "nato", "china", "russia", "iran",
	"israel", "ceasefire", "invasion", "coup", "terrorism", "treaty",
	"ukraine", "taiwan",
}

func init() {
	source.Register("polymarket", func() source.Source { return &Source{} })
}

type Source struct {
	cfg    config.SourceConfig
	client *http.Client
	seen   map[string]time.Time
}

func (s *Source) Name() string                 { return "polymarket" }
func (s *Source) Type() source.SourceType      { return source.SourceTypePoll }
func (s *Source) Interval() time.Duration      { return s.cfg.MarketInterval }
func (s *Source) RequiredCredentials() []string { return nil }
func (s *Source) Stop() error                  { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	if s.cfg.MarketInterval == 0 {
		s.cfg.MarketInterval = 15 * time.Minute
	}
	s.client = &http.Client{Timeout: 15 * time.Second}
	s.seen = make(map[string]time.Time)
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	start := time.Now()

	batch := source.CollectedBatch{
		Source:      "polymarket",
		CollectedAt: time.Now(),
	}

	// Clean old dedup
	cutoff := time.Now().Add(-2 * time.Hour)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}

	// Fetch from Analytics API
	for _, query := range searchQueries {
		markets, err := s.fetchAnalytics(ctx, query)
		if err != nil {
			batch.Errors = append(batch.Errors, fmt.Sprintf("analytics/%s: %s", query, err))
			continue
		}
		batch.Markets = append(batch.Markets, markets...)

		select {
		case <-ctx.Done():
			break
		case <-time.After(pageDelay):
		}
	}

	// Fetch from Gamma API
	gammaMarkets, err := s.fetchGamma(ctx)
	if err != nil {
		batch.Errors = append(batch.Errors, fmt.Sprintf("gamma: %s", err))
	} else {
		batch.Markets = append(batch.Markets, gammaMarkets...)
	}

	// Deduplicate markets by ID
	seen := make(map[string]bool)
	var deduped []source.NormalizedMarket
	for _, m := range batch.Markets {
		if !seen[m.MarketID] {
			seen[m.MarketID] = true
			deduped = append(deduped, m)
		}
	}
	batch.Markets = deduped

	batch.Duration = time.Since(start)
	out <- batch
	return nil
}

func (s *Source) fetchAnalytics(ctx context.Context, query string) ([]source.NormalizedMarket, error) {
	params := url.Values{
		"searchQuery":      {query},
		"selectedStatuses": {"Active"},
		"page":             {"1"},
		"pageSize":         {"100"},
		"sortBy":           {"market_open_interest"},
		"sortOrder":        {"DESC"},
	}

	reqURL := fmt.Sprintf("%s?%s", analyticsURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("analytics returned %d", resp.StatusCode)
	}

	var result analyticsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var markets []source.NormalizedMarket
	for _, m := range result.Data {
		if m.Source != "polymarket" {
			continue
		}
		markets = append(markets, source.NormalizedMarket{
			MarketID:    m.MarketID,
			Question:    m.Question,
			Outcomes:    m.Outcomes,
			Prices:      m.OutcomePrices,
			Volume:      m.Volume,
			Liquidity:   m.Liquidity,
			EndDate:     m.EndDate,
		})
	}
	return markets, nil
}

func (s *Source) fetchGamma(ctx context.Context) ([]source.NormalizedMarket, error) {
	tags := []string{"politics", "current-events", "ukraine", "middle-east", "nato", "china", "russia", "north-korea", "israel", "iran"}

	var allMarkets []source.NormalizedMarket
	for _, tag := range tags {
		params := url.Values{
			"tag_slug":  {tag},
			"limit":     {"100"},
			"active":    {"true"},
			"order":     {"volume"},
			"ascending": {"false"},
		}

		reqURL := fmt.Sprintf("%s?%s", gammaURL, params.Encode())
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			continue
		}

		resp, err := s.client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		var results []gammaMarket
		json.NewDecoder(resp.Body).Decode(&results)
		resp.Body.Close()

		for _, m := range results {
			if m.Closed {
				continue
			}

			var outcomes []string
			json.Unmarshal([]byte(m.Outcomes), &outcomes)

			var priceStrs []string
			json.Unmarshal([]byte(m.OutcomePrices), &priceStrs)

			var prices []float64
			for _, ps := range priceStrs {
				var p float64
				fmt.Sscanf(ps, "%f", &p)
				prices = append(prices, p)
			}

			var tags []string
			for _, t := range m.Tags {
				tags = append(tags, t.Slug)
			}

			allMarkets = append(allMarkets, source.NormalizedMarket{
				MarketID:    m.ID,
				Question:    m.Question,
				Description: truncate(m.Description, 500),
				Outcomes:    outcomes,
				Prices:      prices,
				Volume:      m.VolumeNum,
				Liquidity:   m.LiquidityNum,
				EndDate:     m.EndDateISO,
				Tags:        tags,
			})
		}

		select {
		case <-ctx.Done():
			break
		case <-time.After(pageDelay):
		}
	}

	return allMarkets, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// isGeopoliticallyRelevant checks keyword matches in question text.
func isGeopoliticallyRelevant(question string) bool {
	q := strings.ToLower(question)
	keywords := []string{"war", "military", "invasion", "conflict", "missile", "nuclear",
		"troops", "ceasefire", "strike", "attack", "bomb", "defense", "sanctions",
		"embargo", "treaty", "diplomacy", "summit", "nato", "coup", "regime",
		"election", "president", "intelligence", "espionage", "cyber", "terrorism"}
	for _, k := range keywords {
		if strings.Contains(q, k) {
			return true
		}
	}
	return false
}

// API types

type analyticsResponse struct {
	Data       []analyticsMarket `json:"data"`
	TotalPages int               `json:"totalPages"`
}

type analyticsMarket struct {
	MarketID     string    `json:"market_id"`
	Source       string    `json:"source"`
	Question     string    `json:"question"`
	Outcomes     []string  `json:"outcomes"`
	OutcomePrices []float64 `json:"outcomePrices"`
	Volume       float64   `json:"volume"`
	Liquidity    float64   `json:"liquidity"`
	Active       bool      `json:"active"`
	EndDate      string    `json:"endDate"`
}

type gammaMarket struct {
	ID            string    `json:"id"`
	ConditionID   string    `json:"conditionId"`
	Question      string    `json:"question"`
	Description   string    `json:"description"`
	Outcomes      string    `json:"outcomes"`      // JSON array as string
	OutcomePrices string    `json:"outcomePrices"`  // JSON array as string
	VolumeNum     float64   `json:"volumeNum"`
	LiquidityNum  float64   `json:"liquidityNum"`
	Active        bool      `json:"active"`
	Closed        bool      `json:"closed"`
	EndDateISO    string    `json:"endDateIso"`
	Tags          []gammaTag `json:"tags"`
}

type gammaTag struct {
	Slug  string `json:"slug"`
	Label string `json:"label"`
}
