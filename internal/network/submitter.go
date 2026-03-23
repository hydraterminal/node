package network

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hydraterminal/node/internal/source"
	"github.com/hydraterminal/node/internal/store"
)

// Submitter handles background batch submission to the Hydra network.
type Submitter struct {
	auth      *NodeAuth
	store     store.Store
	batchSize int
	logger    *slog.Logger
	csLog     *CrowdsourceLog
}

// NewSubmitter creates a new network submitter.
func NewSubmitter(auth *NodeAuth, store store.Store, batchSize int, logger *slog.Logger) *Submitter {
	return &Submitter{
		auth:      auth,
		store:     store,
		batchSize: batchSize,
		logger:    logger,
		csLog:     NewCrowdsourceLog(),
	}
}

// CrowdsourceLog returns the submission log for use by the control plane.
func (s *Submitter) CrowdsourceLog() *CrowdsourceLog { return s.csLog }

// SubmitPending reads unsubmitted data from the store and submits it to the backend.
func (s *Submitter) SubmitPending() error {
	batches, err := s.store.GetPendingSubmission(s.batchSize)
	if err != nil {
		return fmt.Errorf("get pending: %w", err)
	}
	if len(batches) == 0 {
		return nil
	}

	for _, batch := range batches {
		receipt, err := s.submitBatch(batch)
		if err != nil {
			s.logger.Error("submission failed", "error", err)
			continue
		}

		s.logger.Info("batch submitted",
			"accepted", receipt.Accepted,
			"rejected", receipt.Rejected,
			"duplicate", receipt.Duplicate,
		)

		if err := s.store.MarkSubmitted(nil); err != nil {
			s.logger.Error("failed to mark submitted", "error", err)
		}
	}

	return nil
}

func (s *Submitter) submitBatch(batch source.CollectedBatch) (*SubmissionReceipt, error) {
	// Convert signals to submission format with hashes
	submitSignals := make([]SubmitSignal, len(batch.Signals))
	for i, sig := range batch.Signals {
		submitSignals[i] = SubmitSignal{
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
	}

	submission := SubmissionBatch{
		NodeID:      s.auth.NodeID(),
		BatchID:     fmt.Sprintf("batch-%d", time.Now().UnixMilli()),
		SubmittedAt: time.Now().UTC(),
		Signals:     submitSignals,
	}

	body, err := json.Marshal(submission)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	url := fmt.Sprintf("%s/nodes/submit", s.auth.BackendURL())
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	resp, err := s.auth.Do(req)
	if err != nil {
		return nil, fmt.Errorf("submit request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("submit returned %d", resp.StatusCode)
	}

	var receipt SubmissionReceipt
	if err := json.NewDecoder(resp.Body).Decode(&receipt); err != nil {
		return nil, fmt.Errorf("decode receipt: %w", err)
	}

	return &receipt, nil
}
