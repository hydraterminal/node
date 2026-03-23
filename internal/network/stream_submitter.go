package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

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
			s.streamSignal(sig)
		}(sig)
	}
}

// streamSignal sends a single signal to the backend crowdsource endpoint
// and records the result in the crowdsource log.
func (s *Submitter) streamSignal(sig source.NormalizedSignal) {
	start := time.Now()

	entry := CrowdsourceEntry{
		Time:      start,
		Title:     sig.Title,
		SourceRef: sig.SourceRef,
		Source:    sig.Source,
	}

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
		entry.Status = "error"
		entry.Error = fmt.Sprintf("marshal: %s", err)
		entry.DurationMs = time.Since(start).Milliseconds()
		s.csLog.Add(entry)
		s.logger.Debug("crowdsource marshal failed", "sourceRef", sig.SourceRef, "error", err)
		return
	}

	url := fmt.Sprintf("%s/nodes/crowdsource", s.auth.BackendURL())
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		entry.Status = "error"
		entry.Error = fmt.Sprintf("new request: %s", err)
		entry.DurationMs = time.Since(start).Milliseconds()
		s.csLog.Add(entry)
		return
	}

	resp, err := s.auth.Do(req)
	if err != nil {
		entry.Status = "error"
		entry.Error = err.Error()
		entry.DurationMs = time.Since(start).Milliseconds()
		s.csLog.Add(entry)
		s.logger.Debug("crowdsource request failed", "sourceRef", sig.SourceRef, "error", err)
		return
	}
	defer resp.Body.Close()

	entry.StatusCode = resp.StatusCode
	entry.DurationMs = time.Since(start).Milliseconds()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		entry.Status = "error"
		entry.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody))
		s.csLog.Add(entry)
		s.logger.Debug("crowdsource backend error", "sourceRef", sig.SourceRef, "status", resp.StatusCode, "body", string(respBody))
		return
	}

	var receipt CrowdsourceReceipt
	if err := json.Unmarshal(respBody, &receipt); err == nil {
		entry.Status = receipt.Status
		entry.SignalID = receipt.SignalID
		if receipt.Reason != "" {
			entry.Error = receipt.Reason
		}
	} else {
		entry.Status = "new"
	}

	s.csLog.Add(entry)
	s.logger.Debug("crowdsource submitted", "title", sig.Title, "status", entry.Status, "signalId", entry.SignalID, "duration_ms", entry.DurationMs)
}
