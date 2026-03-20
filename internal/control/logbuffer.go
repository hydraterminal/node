package control

import (
	"sync"
	"time"
)

// LogEntry represents a single log line.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

// LogBuffer is a thread-safe ring buffer for log entries.
type LogBuffer struct {
	entries []LogEntry
	size    int
	pos     int
	full    bool
	mu      sync.RWMutex

	// Subscribers receive new log entries in real time.
	subs map[chan LogEntry]struct{}
	subsMu sync.RWMutex
}

// NewLogBuffer creates a log buffer with the given capacity.
func NewLogBuffer(size int) *LogBuffer {
	return &LogBuffer{
		entries: make([]LogEntry, size),
		size:    size,
		subs:    make(map[chan LogEntry]struct{}),
	}
}

// Write adds a log entry to the buffer and notifies subscribers.
func (b *LogBuffer) Write(entry LogEntry) {
	b.mu.Lock()
	b.entries[b.pos] = entry
	b.pos = (b.pos + 1) % b.size
	if b.pos == 0 {
		b.full = true
	}
	b.mu.Unlock()

	// Fan-out to subscribers (non-blocking).
	b.subsMu.RLock()
	for ch := range b.subs {
		select {
		case ch <- entry:
		default:
			// Subscriber too slow, drop entry.
		}
	}
	b.subsMu.RUnlock()
}

// Subscribe returns a channel that receives new log entries as they are written.
// The caller must call Unsubscribe when done to avoid leaking resources.
func (b *LogBuffer) Subscribe(ch chan LogEntry) {
	b.subsMu.Lock()
	b.subs[ch] = struct{}{}
	b.subsMu.Unlock()
}

// Unsubscribe removes a subscriber channel. It does NOT close the channel.
func (b *LogBuffer) Unsubscribe(ch chan LogEntry) {
	b.subsMu.Lock()
	delete(b.subs, ch)
	b.subsMu.Unlock()
}

// Get returns log entries, optionally filtered by level and source.
func (b *LogBuffer) Get(level, source string, limit int) []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var all []LogEntry
	if b.full {
		// Read from pos to end, then start to pos
		for i := b.pos; i < b.size; i++ {
			all = append(all, b.entries[i])
		}
		for i := 0; i < b.pos; i++ {
			all = append(all, b.entries[i])
		}
	} else {
		all = b.entries[:b.pos]
	}

	// Filter
	var filtered []LogEntry
	for _, e := range all {
		if level != "" && e.Level != level {
			continue
		}
		if source != "" && e.Source != source {
			continue
		}
		filtered = append(filtered, e)
	}

	// Return last N entries
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	return filtered
}
