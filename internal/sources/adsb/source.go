package adsb

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

const defaultAPIHost = "adsbexchange-com1.p.rapidapi.com"

func init() {
	source.Register("adsb", func() source.Source { return &Source{} })
}

type Source struct {
	cfg     config.SourceConfig
	apiKey  string
	apiHost string
	client  *http.Client
}

func (s *Source) Name() string                 { return "adsb" }
func (s *Source) Type() source.SourceType      { return source.SourceTypePoll }
func (s *Source) Interval() time.Duration      { return s.cfg.Interval }
func (s *Source) RequiredCredentials() []string { return []string{"ADSB_API_KEY"} }
func (s *Source) Stop() error                  { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	if s.cfg.Interval == 0 {
		s.cfg.Interval = 60 * time.Second
	}
	s.apiKey = config.GetSourceAPIKey(cfg.APIKeyEnv)
	if s.apiKey == "" {
		s.apiKey = config.GetSourceAPIKey("ADSB_API_KEY")
	}
	s.apiHost = config.GetSourceAPIKey(cfg.APIHostEnv)
	if s.apiHost == "" {
		s.apiHost = defaultAPIHost
	}
	s.client = &http.Client{Timeout: 15 * time.Second}
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	start := time.Now()

	url := fmt.Sprintf("https://%s/v2/mil", s.apiHost)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-rapidapi-key", s.apiKey)
	req.Header.Set("x-rapidapi-host", s.apiHost)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch ADS-B: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ADS-B returned %d", resp.StatusCode)
	}

	var result adsbResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode ADS-B: %w", err)
	}

	batch := source.CollectedBatch{
		Source:      "adsb",
		CollectedAt: time.Now(),
		Duration:    time.Since(start),
	}

	for _, ac := range result.Aircraft {
		// Skip aircraft without position
		if ac.Lat == 0 && ac.Lon == 0 {
			continue
		}
		// Skip stale positions (>60s)
		if ac.SeenPos > 60 {
			continue
		}

		category := classifyAircraft(ac)

		batch.Aircraft = append(batch.Aircraft, source.NormalizedAircraft{
			Hex:          ac.Hex,
			Callsign:     strings.TrimSpace(ac.Flight),
			Type:         ac.Type,
			Registration: ac.Registration,
			Operator:     ac.OwnOp,
			Category:     category,
			Latitude:     ac.Lat,
			Longitude:    ac.Lon,
			Altitude:     ac.AltBaro,
			Speed:        ac.GroundSpeed,
			Track:        ac.Track,
			VerticalRate: ac.BaroRate,
			Squawk:       ac.Squawk,
			Emergency:    ac.Emergency,
			Flag:         "", // Derived from registration if needed
		})
	}

	out <- batch
	return nil
}

func classifyAircraft(ac adsbAircraft) string {
	// Classification by ICAO type code
	t := strings.ToUpper(ac.Type)
	if cat, ok := typeClassification[t]; ok {
		return cat
	}

	// Fallback: classify by callsign patterns if military
	callsign := strings.ToUpper(strings.TrimSpace(ac.Flight))
	if callsign == "" {
		return "UNKNOWN"
	}

	for prefix, cat := range callsignPatterns {
		if strings.HasPrefix(callsign, prefix) {
			return cat
		}
	}

	return "UNKNOWN"
}

// Callsign → category patterns
var callsignPatterns = map[string]string{
	"RCH":   "TRANSPORT",
	"REACH": "TRANSPORT",
	"EVAC":  "VIP",
	"SAM":   "VIP",
	"EXEC":  "VIP",
	"SPAR":  "VIP",
	"VENUS": "VIP",
	"DARK":  "ISR",
	"JAKE":  "ISR",
	"HOMER": "ISR",
	"DRAGN": "ISR",
	"TOPCT": "ISR",
	"IRON":  "TANKER",
	"ETHYL": "TANKER",
	"PEARL": "TANKER",
	"PACK":  "TANKER",
	"NCHO":  "TANKER",
	"COBRA": "FIGHTER",
	"VIPER": "FIGHTER",
	"HAWG":  "FIGHTER",
	"BONE":  "BOMBER",
	"DEATH": "BOMBER",
	"DOOM":  "BOMBER",
}

// ICAO type code → category (subset of most important)
var typeClassification = map[string]string{
	// Fighters
	"F16":  "FIGHTER",
	"F15":  "FIGHTER",
	"F18":  "FIGHTER",
	"FA18": "FIGHTER",
	"F22":  "FIGHTER",
	"F35":  "FIGHTER",
	"F14":  "FIGHTER",
	"EUFI": "FIGHTER",
	"RFAL": "FIGHTER",
	"GRPN": "FIGHTER",
	"SU27": "FIGHTER",
	"SU30": "FIGHTER",
	"SU35": "FIGHTER",
	"MG29": "FIGHTER",
	"JF17": "FIGHTER",
	"J10":  "FIGHTER",

	// Bombers
	"B1":   "BOMBER",
	"B1B":  "BOMBER",
	"B2":   "BOMBER",
	"B52":  "BOMBER",
	"B52H": "BOMBER",
	"TU95": "BOMBER",
	"TU160": "BOMBER",

	// Transport
	"C17":  "TRANSPORT",
	"C130": "TRANSPORT",
	"C5":   "TRANSPORT",
	"C5M":  "TRANSPORT",
	"C2":   "TRANSPORT",
	"C160": "TRANSPORT",
	"A400": "TRANSPORT",
	"IL76": "TRANSPORT",
	"AN12": "TRANSPORT",
	"AN22": "TRANSPORT",
	"AN124": "TRANSPORT",
	"AN225": "TRANSPORT",
	"KC10": "TANKER",
	"KC46": "TANKER",
	"KC135": "TANKER",
	"KC30": "TANKER",
	"A310": "TANKER",

	// ISR
	"E3":   "ISR",
	"E3CF": "ISR",
	"E6":   "ISR",
	"E8":   "ISR",
	"RC135": "ISR",
	"U2":   "ISR",
	"RQ4":  "ISR",
	"MQ9":  "ISR",
	"MQ1":  "ISR",
	"P3":   "ISR",
	"P8":   "ISR",
	"EP3":  "ISR",
	"GLEX": "ISR",

	// Helicopters
	"H60":  "HELICOPTER",
	"UH60": "HELICOPTER",
	"AH64": "HELICOPTER",
	"CH47": "HELICOPTER",
	"CH53": "HELICOPTER",
	"V22":  "HELICOPTER",
	"MH60": "HELICOPTER",

	// VIP
	"VC25": "VIP",
	"C32":  "VIP",
	"C37":  "VIP",
	"C40":  "VIP",
}

// ADS-B API types

type adsbResponse struct {
	Aircraft []adsbAircraft `json:"ac"`
	Total    int            `json:"total"`
	Now      float64        `json:"now"`
}

type adsbAircraft struct {
	Hex          string  `json:"hex"`
	Type         string  `json:"t"`
	Flight       string  `json:"flight"`
	Registration string  `json:"r"`
	Desc         string  `json:"desc"`
	OwnOp        string  `json:"ownOp"`
	Lat          float64 `json:"lat"`
	Lon          float64 `json:"lon"`
	AltBaro      float64 `json:"alt_baro"`
	AltGeom      float64 `json:"alt_geom"`
	GroundSpeed  float64 `json:"gs"`
	Track        float64 `json:"track"`
	BaroRate     float64 `json:"baro_rate"`
	Squawk       string  `json:"squawk"`
	Emergency    string  `json:"emergency"`
	Category     string  `json:"category"`
	DbFlags      int     `json:"dbFlags"`
	RSSI         float64 `json:"rssi"`
	Seen         float64 `json:"seen"`
	SeenPos      float64 `json:"seen_pos"`
}
