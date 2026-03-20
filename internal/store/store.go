package store

import (
	"github.com/hydraterminal/node/internal/source"
)

// Store is the interface for local data persistence.
type Store interface {
	// Init creates tables and prepares the database.
	Init() error

	// Close shuts down the store.
	Close() error

	// InsertBatch writes a collected batch to local storage.
	InsertBatch(batch source.CollectedBatch) error

	// GetSignals returns recent signals, optionally filtered.
	GetSignals(opts SignalQuery) ([]source.NormalizedSignal, error)

	// GetAircraft returns recent aircraft positions.
	GetAircraft() ([]source.NormalizedAircraft, error)

	// GetVessels returns recent vessel positions.
	GetVessels() ([]source.NormalizedVessel, error)

	// GetMarkets returns recent market data.
	GetMarkets() ([]source.NormalizedMarket, error)

	// GetPosts returns recent social media posts.
	GetPosts(platform string) ([]source.NormalizedPost, error)

	// GetAlerts returns active alerts.
	GetAlerts() ([]source.NormalizedAlert, error)

	// GetPendingSubmission returns batches not yet submitted to the network.
	GetPendingSubmission(limit int) ([]source.CollectedBatch, error)

	// MarkSubmitted marks batches as submitted.
	MarkSubmitted(batchIDs []string) error

	// Prune deletes data older than the retention period.
	Prune() error
}

// SignalQuery provides filtering options for signal queries.
type SignalQuery struct {
	Category string
	Severity string
	Source   string
	Limit    int
	Offset   int
}
