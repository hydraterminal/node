package source

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// Hash returns the SHA-256 hash of the canonical JSON representation of a signal.
// Used for cross-node validation without exposing raw sources.
func (s *NormalizedSignal) Hash() string {
	canonical := map[string]any{
		"title":       s.Title,
		"description": s.Description,
		"severity":    s.Severity,
		"category":    s.Category,
		"source":      s.Source,
		"sourceRef":   s.SourceRef,
		"collectedAt": s.CollectedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if s.Latitude != nil {
		canonical["latitude"] = *s.Latitude
	}
	if s.Longitude != nil {
		canonical["longitude"] = *s.Longitude
	}
	if s.Region != "" {
		canonical["region"] = s.Region
	}
	if len(s.Countries) > 0 {
		sorted := make([]string, len(s.Countries))
		copy(sorted, s.Countries)
		sort.Strings(sorted)
		canonical["countries"] = sorted
	}

	data, _ := json.Marshal(canonical)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// TotalSignals returns the total number of signals in a batch,
// including signals derived from aircraft, vessels, alerts, etc.
func (b *CollectedBatch) TotalSignals() int {
	return len(b.Signals)
}

// HasErrors returns true if the batch encountered any errors.
func (b *CollectedBatch) HasErrors() bool {
	return len(b.Errors) > 0
}
