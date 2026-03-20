package airspace

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const (
	awcSigmetURL  = "https://aviationweather.gov/api/data/airsigmet?format=geojson&type=S"
	awcAirmetURL  = "https://aviationweather.gov/api/data/airsigmet?format=geojson&type=A"
	openaipBaseURL = "https://api.core.openaip.net/api"
)

func init() {
	source.Register("airspace", func() source.Source { return &Source{} })
}

type Source struct {
	cfg         config.SourceConfig
	openaipKey  string
	client      *http.Client
	seen        map[string]time.Time
}

func (s *Source) Name() string                 { return "airspace" }
func (s *Source) Type() source.SourceType      { return source.SourceTypePoll }
func (s *Source) Interval() time.Duration      { return s.cfg.Interval }
func (s *Source) RequiredCredentials() []string { return nil } // OpenAIP optional
func (s *Source) Stop() error                  { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	if s.cfg.Interval == 0 {
		s.cfg.Interval = 10 * time.Minute
	}
	s.openaipKey = config.GetSourceAPIKey(cfg.OpenaipKeyEnv)
	if s.openaipKey == "" {
		s.openaipKey = config.GetSourceAPIKey("OPENAIP_API_KEY")
	}
	s.client = &http.Client{Timeout: 15 * time.Second}
	s.seen = make(map[string]time.Time)
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	start := time.Now()

	batch := source.CollectedBatch{
		Source:      "airspace",
		CollectedAt: time.Now(),
	}

	// Clean old dedup
	cutoff := time.Now().Add(-24 * time.Hour)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}

	// Fetch AWC SIGMETs
	sigmets, err := s.fetchAWC(ctx, awcSigmetURL)
	if err != nil {
		batch.Errors = append(batch.Errors, fmt.Sprintf("AWC SIGMET: %s", err))
	} else {
		batch.Signals = append(batch.Signals, sigmets...)
	}

	// Fetch AWC AIRMETs
	airmets, err := s.fetchAWC(ctx, awcAirmetURL)
	if err != nil {
		batch.Errors = append(batch.Errors, fmt.Sprintf("AWC AIRMET: %s", err))
	} else {
		batch.Signals = append(batch.Signals, airmets...)
	}

	// Fetch OpenAIP if key is available
	if s.openaipKey != "" {
		airspaces, err := s.fetchOpenAIP(ctx)
		if err != nil {
			batch.Errors = append(batch.Errors, fmt.Sprintf("OpenAIP: %s", err))
		} else {
			batch.Signals = append(batch.Signals, airspaces...)
		}
	}

	batch.Duration = time.Since(start)
	out <- batch
	return nil
}

func (s *Source) fetchAWC(ctx context.Context, url string) ([]source.NormalizedSignal, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AWC returned %d", resp.StatusCode)
	}

	var feed geoJSONCollection
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, err
	}

	var signals []source.NormalizedSignal
	for _, f := range feed.Features {
		ref := fmt.Sprintf("awc-%s-%s", f.Properties.SeriesID, f.Properties.ValidTimeFrom)
		if _, dup := s.seen[ref]; dup {
			continue
		}
		s.seen[ref] = time.Now()

		lat, lng := centroid(f.Geometry.Coordinates)
		sigType := f.Properties.AirSigmetType
		hazard := f.Properties.Hazard

		sig := source.NormalizedSignal{
			Title:       fmt.Sprintf("%s: %s", sigType, hazard),
			Description: f.Properties.RawAirSigmet,
			Severity:    "LOW",
			Category:    "AIRSPACE",
			Source:      "SYSTEM",
			SourceRef:   ref,
			Latitude:    &lat,
			Longitude:   &lng,
			CollectedAt: time.Now(),
			Metadata: map[string]any{
				"provider":      "awc",
				"type":          sigType,
				"hazard":        hazard,
				"seriesId":      f.Properties.SeriesID,
				"validTimeFrom": f.Properties.ValidTimeFrom,
				"validTimeTo":   f.Properties.ValidTimeTo,
				"altitudeHigh":  f.Properties.AltitudeHi1,
				"altitudeLow":   f.Properties.AltitudeLow1,
				"geometry":      f.Geometry,
			},
		}
		signals = append(signals, sig)
	}
	return signals, nil
}

func (s *Source) fetchOpenAIP(ctx context.Context) ([]source.NormalizedSignal, error) {
	regions := []string{
		"IL,PS,LB,SY,JO,IQ,IR,SA,YE,AE,OM,KW,BH,QA",
		"DE,FR,GB,IT,ES,PL,NL,BE,AT,CH,CZ,SE,NO,FI,DK",
		"CN,JP,KR,KP,TW,IN,PK,AF",
		"US,CA,MX",
	}

	var allSignals []source.NormalizedSignal
	for _, region := range regions {
		signals, err := s.fetchOpenAIPRegion(ctx, region)
		if err != nil {
			continue
		}
		allSignals = append(allSignals, signals...)
	}
	return allSignals, nil
}

func (s *Source) fetchOpenAIPRegion(ctx context.Context, countries string) ([]source.NormalizedSignal, error) {
	url := fmt.Sprintf("%s/airspaces?type=1,2,3,9,17,18,31&country=%s&limit=200&page=1", openaipBaseURL, countries)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-openaip-api-key", s.openaipKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAIP returned %d", resp.StatusCode)
	}

	var result openaipResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var signals []source.NormalizedSignal
	for _, item := range result.Items {
		ref := fmt.Sprintf("openaip-%s", item.ID)
		if _, dup := s.seen[ref]; dup {
			continue
		}
		s.seen[ref] = time.Now()

		severity := openaipTypeSeverity(item.Type)
		typeName := openaipTypeName(item.Type)
		lat, lng := centroid(item.Geometry.Coordinates)

		sig := source.NormalizedSignal{
			Title:       fmt.Sprintf("%s: %s", typeName, item.Name),
			Description: item.Name,
			Severity:    severity,
			Category:    "AIRSPACE",
			Source:      "SYSTEM",
			SourceRef:   ref,
			Countries:   []string{strings.ToUpper(item.Country)},
			Latitude:    &lat,
			Longitude:   &lng,
			CollectedAt: time.Now(),
			Metadata: map[string]any{
				"provider":      "openaip",
				"notamId":       item.ID,
				"type":          typeName,
				"geometry":      item.Geometry,
				"altitudeUpper": formatAltitude(item.UpperLimit),
				"altitudeLower": formatAltitude(item.LowerLimit),
			},
		}
		signals = append(signals, sig)
	}
	return signals, nil
}

func openaipTypeSeverity(t int) string {
	switch t {
	case 3, 31: // PROHIBITED, TFR
		return "HIGH"
	case 1, 2, 9: // RESTRICTED, DANGER, TSA
		return "MEDIUM"
	default: // WARNING, ALERT
		return "LOW"
	}
}

func openaipTypeName(t int) string {
	switch t {
	case 1:
		return "RESTRICTED"
	case 2:
		return "DANGER"
	case 3:
		return "PROHIBITED"
	case 9:
		return "TSA"
	case 17:
		return "ALERT"
	case 18:
		return "WARNING"
	case 31:
		return "TFR"
	default:
		return "UNKNOWN"
	}
}

func formatAltitude(a openaipAltitude) string {
	if a.Unit == 6 {
		return fmt.Sprintf("FL%d", a.Value)
	}
	ref := "GND"
	if a.ReferenceDatum == 2 {
		ref = "STD"
	}
	return fmt.Sprintf("%d M %s", a.Value, ref)
}

func centroid(coords [][][]float64) (float64, float64) {
	if len(coords) == 0 || len(coords[0]) == 0 {
		return 0, 0
	}
	ring := coords[0]
	var sumLat, sumLng float64
	for _, pt := range ring {
		if len(pt) >= 2 {
			sumLng += pt[0]
			sumLat += pt[1]
		}
	}
	n := float64(len(ring))
	return math.Round(sumLat/n*1e6) / 1e6, math.Round(sumLng/n*1e6) / 1e6
}

// API types

type geoJSONCollection struct {
	Features []awcFeature `json:"features"`
}

type awcFeature struct {
	Geometry   polygonGeometry `json:"geometry"`
	Properties awcProperties   `json:"properties"`
}

type polygonGeometry struct {
	Type        string        `json:"type"`
	Coordinates [][][]float64 `json:"coordinates"`
}

type awcProperties struct {
	IcaoID        string `json:"icaoId"`
	AirSigmetType string `json:"airSigmetType"`
	Hazard        string `json:"hazard"`
	SeriesID      string `json:"seriesId"`
	ValidTimeFrom string `json:"validTimeFrom"`
	ValidTimeTo   string `json:"validTimeTo"`
	AltitudeHi1   int    `json:"altitudeHi1"`
	AltitudeLow1  int    `json:"altitudeLow1"`
	RawAirSigmet  string `json:"rawAirSigmet"`
}

type openaipResponse struct {
	Items      []openaipAirspace `json:"items"`
	TotalCount int               `json:"totalCount"`
}

type openaipAirspace struct {
	ID         string          `json:"_id"`
	Name       string          `json:"name"`
	Type       int             `json:"type"`
	Country    string          `json:"country"`
	Geometry   polygonGeometry `json:"geometry"`
	UpperLimit openaipAltitude `json:"upperLimit"`
	LowerLimit openaipAltitude `json:"lowerLimit"`
}

type openaipAltitude struct {
	Value          int `json:"value"`
	Unit           int `json:"unit"`
	ReferenceDatum int `json:"referenceDatum"`
}
