package network

import (
	"sync"
	"time"
)

const maxCrowdsourceLogEntries = 500

// CrowdsourceEntry records a single crowdsource submission attempt.
type CrowdsourceEntry struct {
	Time       time.Time `json:"time"`
	Title      string    `json:"title"`
	SourceRef  string    `json:"sourceRef"`
	Source     string    `json:"source"`     // TELEGRAM, TWITTER, etc.
	Status     string    `json:"status"`     // "new" | "confirmed" | "error"
	SignalID   string    `json:"signalId,omitempty"`
	StatusCode int       `json:"statusCode"` // HTTP status from backend
	Error      string    `json:"error,omitempty"`
	DurationMs int64     `json:"durationMs"`
}

// CrowdsourceLog is a fixed-size ring buffer of crowdsource submission entries.
type CrowdsourceLog struct {
	mu      sync.Mutex
	entries []CrowdsourceEntry
}

func NewCrowdsourceLog() *CrowdsourceLog {
	return &CrowdsourceLog{
		entries: make([]CrowdsourceEntry, 0, maxCrowdsourceLogEntries),
	}
}

func (l *CrowdsourceLog) Add(e CrowdsourceEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	if len(l.entries) > maxCrowdsourceLogEntries {
		l.entries = l.entries[len(l.entries)-maxCrowdsourceLogEntries:]
	}
}

// Entries returns all entries newest-first.
func (l *CrowdsourceLog) Entries() []CrowdsourceEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]CrowdsourceEntry, len(l.entries))
	for i, e := range l.entries {
		out[len(l.entries)-1-i] = e
	}
	return out
}

// Stats returns aggregate counts over the entire log.
func (l *CrowdsourceLog) Stats() (total, newCount, confirmed, errors int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		total++
		switch e.Status {
		case "new":
			newCount++
		case "confirmed":
			confirmed++
		case "error":
			errors++
		}
	}
	return
}
