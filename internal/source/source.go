package source

import (
	"context"
	"time"

	"github.com/hydraterminal/node/internal/config"
)

// SourceType distinguishes polling sources from persistent stream sources.
type SourceType int

const (
	// SourceTypePoll is called periodically by the scheduler.
	SourceTypePoll SourceType = iota
	// SourceTypeStream runs continuously (e.g., WebSocket connections).
	SourceTypeStream
)

// Source is the interface every data source plugin must implement.
type Source interface {
	// Name returns the unique identifier (e.g., "adsb", "ais", "usgs").
	Name() string

	// Type returns whether this source is polled or streams continuously.
	Type() SourceType

	// Init validates credentials and prepares the source.
	Init(cfg config.SourceConfig) error

	// Start begins data collection. For poll sources the scheduler calls this
	// periodically. For stream sources this runs until ctx is cancelled.
	// Collected data is pushed to the out channel.
	Start(ctx context.Context, out chan<- CollectedBatch) error

	// Stop gracefully shuts down the source, closing connections.
	Stop() error

	// Interval returns the polling interval. Only meaningful for SourceTypePoll.
	Interval() time.Duration

	// RequiredCredentials returns the env var names this source needs.
	RequiredCredentials() []string
}

// CollectedBatch is the normalized output of a single collection cycle.
type CollectedBatch struct {
	Source      string              `json:"source"`
	Signals    []NormalizedSignal   `json:"signals,omitempty"`
	Aircraft   []NormalizedAircraft `json:"aircraft,omitempty"`
	Vessels    []NormalizedVessel   `json:"vessels,omitempty"`
	Alerts     []NormalizedAlert    `json:"alerts,omitempty"`
	Markets    []NormalizedMarket   `json:"markets,omitempty"`
	Posts      []NormalizedPost     `json:"posts,omitempty"`
	CollectedAt time.Time           `json:"collectedAt"`
	Duration    time.Duration       `json:"durationMs"`
	Errors      []string            `json:"errors,omitempty"`
}

// NormalizedSignal is the universal signal format submitted to the network.
type NormalizedSignal struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Severity    string            `json:"severity"`
	Category    string            `json:"category"`
	Source      string            `json:"source"`
	SourceRef   string            `json:"sourceRef"`
	Region      string            `json:"region,omitempty"`
	Countries   []string          `json:"countries,omitempty"`
	Latitude    *float64          `json:"latitude,omitempty"`
	Longitude   *float64          `json:"longitude,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
	ExpiresAt   *time.Time        `json:"expiresAt,omitempty"`
	CollectedAt time.Time         `json:"collectedAt"`
}

// NormalizedAircraft represents a military aircraft position.
type NormalizedAircraft struct {
	Hex          string  `json:"hex"`
	Callsign     string  `json:"callsign,omitempty"`
	Type         string  `json:"type,omitempty"`
	Registration string  `json:"registration,omitempty"`
	Operator     string  `json:"operator,omitempty"`
	Category     string  `json:"category,omitempty"`
	Latitude     float64 `json:"lat"`
	Longitude    float64 `json:"lng"`
	Altitude     float64 `json:"altitude"`
	Speed        float64 `json:"speed"`
	Track        float64 `json:"track"`
	VerticalRate float64 `json:"verticalRate"`
	Squawk       string  `json:"squawk,omitempty"`
	Emergency    string  `json:"emergency,omitempty"`
	Flag         string  `json:"flag,omitempty"`
}

// NormalizedVessel represents a tracked vessel.
type NormalizedVessel struct {
	MMSI        string  `json:"mmsi"`
	Name        string  `json:"name,omitempty"`
	CallSign    string  `json:"callSign,omitempty"`
	IMO         string  `json:"imo,omitempty"`
	ShipType    int     `json:"shipType"`
	Category    string  `json:"category,omitempty"`
	Flag        string  `json:"flag,omitempty"`
	Destination string  `json:"destination,omitempty"`
	Latitude    float64 `json:"lat"`
	Longitude   float64 `json:"lng"`
	Speed       float64 `json:"speed"`
	Course      float64 `json:"course"`
	Heading     float64 `json:"heading"`
	NavStatus   int     `json:"navStatus"`
}

// NormalizedAlert represents a real-time alert (e.g., missile alert).
type NormalizedAlert struct {
	ID          string   `json:"id"`
	AlertType   string   `json:"alertType"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Severity    string   `json:"severity"`
	Cities      []string `json:"cities,omitempty"`
	Latitude    float64  `json:"lat"`
	Longitude   float64  `json:"lng"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// NormalizedMarket represents a prediction market.
type NormalizedMarket struct {
	MarketID    string   `json:"marketId"`
	Question    string   `json:"question"`
	Description string   `json:"description,omitempty"`
	Outcomes    []string `json:"outcomes"`
	Prices      []float64 `json:"prices"`
	Volume      float64  `json:"volume"`
	Liquidity   float64  `json:"liquidity"`
	EndDate     string   `json:"endDate,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// NormalizedPost represents a social media post (X/Telegram).
type NormalizedPost struct {
	ID        string   `json:"id"`
	Platform  string   `json:"platform"` // "twitter" or "telegram"
	Author    string   `json:"author"`
	Text      string   `json:"text"`
	URL       string   `json:"url,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Likes     int      `json:"likes,omitempty"`
	Retweets  int      `json:"retweets,omitempty"`
	Views     int      `json:"views,omitempty"`
	Photos    []string `json:"photos,omitempty"`
}
