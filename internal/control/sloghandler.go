package control

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// BufferHandler is an slog.Handler that writes log entries into a LogBuffer
// in addition to forwarding them to a wrapped handler (e.g. stdout).
type BufferHandler struct {
	inner  slog.Handler
	buffer *LogBuffer
	source string // optional source prefix from WithGroup/WithAttrs
}

// NewBufferHandler wraps an existing handler and tees entries to the buffer.
func NewBufferHandler(inner slog.Handler, buffer *LogBuffer) *BufferHandler {
	return &BufferHandler{inner: inner, buffer: buffer}
}

func (h *BufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *BufferHandler) Handle(ctx context.Context, r slog.Record) error {
	// Build message with attrs
	var sb strings.Builder
	sb.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		sb.WriteString(" ")
		sb.WriteString(a.Key)
		sb.WriteString("=")
		sb.WriteString(a.Value.String())
		return true
	})

	level := "info"
	switch {
	case r.Level >= slog.LevelError:
		level = "error"
	case r.Level >= slog.LevelWarn:
		level = "warn"
	case r.Level >= slog.LevelDebug:
		level = "debug"
	}

	h.buffer.Write(LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Source:    h.source,
		Message:   sb.String(),
	})

	return h.inner.Handle(ctx, r)
}

func (h *BufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Extract "source" attr if present for the Source field
	src := h.source
	for _, a := range attrs {
		if a.Key == "source" {
			src = a.Value.String()
		}
	}
	return &BufferHandler{
		inner:  h.inner.WithAttrs(attrs),
		buffer: h.buffer,
		source: src,
	}
}

func (h *BufferHandler) WithGroup(name string) slog.Handler {
	return &BufferHandler{
		inner:  h.inner.WithGroup(name),
		buffer: h.buffer,
		source: h.source,
	}
}
