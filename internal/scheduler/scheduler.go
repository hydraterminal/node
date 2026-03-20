package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hydraterminal/node/internal/source"
)

// Scheduler manages periodic execution of poll-type sources
// and lifecycle of stream-type sources.
type Scheduler struct {
	sources []source.Source
	out     chan<- source.CollectedBatch
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	logger  *slog.Logger
}

// New creates a scheduler for the given sources.
func New(sources []source.Source, out chan<- source.CollectedBatch, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		sources: sources,
		out:     out,
		logger:  logger,
	}
}

// Start begins all sources. Poll sources run on their interval.
// Stream sources run continuously until stopped.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)

	for _, src := range s.sources {
		switch src.Type() {
		case source.SourceTypePoll:
			s.startPollSource(ctx, src)
		case source.SourceTypeStream:
			s.startStreamSource(ctx, src)
		}
	}

	names := make([]string, len(s.sources))
	for i, src := range s.sources {
		names[i] = src.Name()
	}
	s.logger.Info("scheduler started", "sources", names, "count", len(s.sources))
}

// Stop gracefully shuts down all sources.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.logger.Info("scheduler stopped")
}

func (s *Scheduler) startPollSource(ctx context.Context, src source.Source) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		name := src.Name()
		interval := src.Interval()

		// Run immediately on start
		s.runPoll(ctx, src)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				s.logger.Info("stopping poll source", "source", name)
				if err := src.Stop(); err != nil {
					s.logger.Error("error stopping source", "source", name, "error", err)
				}
				return
			case <-ticker.C:
				s.runPoll(ctx, src)
			}
		}
	}()
}

func (s *Scheduler) runPoll(ctx context.Context, src source.Source) {
	name := src.Name()
	start := time.Now()

	s.logger.Debug("polling source", "source", name)

	if err := src.Start(ctx, s.out); err != nil {
		s.logger.Error("poll failed", "source", name, "error", err, "duration", time.Since(start))
		return
	}

	s.logger.Debug("poll complete", "source", name, "duration", time.Since(start))
}

func (s *Scheduler) startStreamSource(ctx context.Context, src source.Source) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		name := src.Name()
		s.logger.Info("starting stream source", "source", name)

		for {
			err := src.Start(ctx, s.out)
			if ctx.Err() != nil {
				// Context cancelled, shutting down
				s.logger.Info("stopping stream source", "source", name)
				src.Stop()
				return
			}

			s.logger.Error("stream source disconnected, reconnecting", "source", name, "error", err)

			// Exponential backoff: wait before reconnecting
			select {
			case <-ctx.Done():
				src.Stop()
				return
			case <-time.After(5 * time.Second):
				// Reconnect
			}
		}
	}()
}

// SourceStatus represents the current state of a source.
type SourceStatus struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Running   bool      `json:"running"`
	LastPoll  time.Time `json:"lastPoll,omitempty"`
	ErrorCount int      `json:"errorCount"`
}

// Status returns the status of all managed sources.
func (s *Scheduler) Status() []SourceStatus {
	statuses := make([]SourceStatus, len(s.sources))
	for i, src := range s.sources {
		typ := "poll"
		if src.Type() == source.SourceTypeStream {
			typ = "stream"
		}
		statuses[i] = SourceStatus{
			Name:    src.Name(),
			Type:    typ,
			Running: true, // simplified for now
		}
	}
	return statuses
}

// RestartSource stops and restarts a single source by name.
func (s *Scheduler) RestartSource(ctx context.Context, name string) error {
	for _, src := range s.sources {
		if src.Name() == name {
			s.logger.Info("restarting source", "source", name)
			if err := src.Stop(); err != nil {
				return fmt.Errorf("stop %s: %w", name, err)
			}
			// Re-start based on type
			switch src.Type() {
			case source.SourceTypePoll:
				s.startPollSource(ctx, src)
			case source.SourceTypeStream:
				s.startStreamSource(ctx, src)
			}
			return nil
		}
	}
	return fmt.Errorf("source not found: %s", name)
}
