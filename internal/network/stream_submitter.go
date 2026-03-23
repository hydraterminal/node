package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hydraterminal/node/internal/source"
)

// CrowdsourceReceipt is returned by the backend's /nodes/crowdsource endpoint.
type CrowdsourceReceipt struct {
	Status   string `json:"status"`   // "new" | "confirmed" | "rejected"
	SignalID string `json:"signalId"` // ID of the CrowdsourceSignal
	Reason   string `json:"reason,omitempty"`
}

// StreamSignals immediately submits all signals from a batch to the crowdsource
// endpoint, one per HTTP request. This runs concurrently so it does not block
// the data pipeline. Errors are logged but never fatal.
func (s *Submitter) StreamSignals(signals []source.NormalizedSignal) {
	for _, sig := range signals {
		go func(sig source.NormalizedSignal) {
			if err := s.streamSignal(sig); err != nil {
				s.logger.Debug("crowdsource stream failed", "sourceRef", sig.SourceRef, "error", err)
			}
		}(sig)
	}
}

// streamSignal sends a single signal to the backend crowdsource endpoint.
func (s *Submitter) streamSignal(sig source.NormalizedSignal) error {
	payload := SubmitSignal{
		Title:       sig.Title,
		Description: sig.Description,
		Severity:    sig.Severity,
		Category:    sig.Category,
		Source:      sig.Source,
		SourceRef:   sig.SourceRef,
		Region:      sig.Region,
		Countries:   sig.Countries,
		Latitude:    sig.Latitude,
		Longitude:   sig.Longitude,
		Metadata:    sig.Metadata,
		CollectedAt: sig.CollectedAt,
		Hash:        sig.Hash(),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	url := fmt.Sprintf("%s/nodes/crowdsource", s.auth.BackendURL())
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	// Use the auth client which sets node credentials on the request
	resp, err := s.auth.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("backend returned %d", resp.StatusCode)
	}

	return nil
}
