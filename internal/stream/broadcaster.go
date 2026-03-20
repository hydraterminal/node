package stream

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
)

// Message is the JSON envelope sent to WebSocket clients.
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Client represents a connected WebSocket client.
type Client struct {
	conn   *websocket.Conn
	send   chan []byte
	scopes []string // empty = all scopes (public), non-empty = filtered
	ip     string
}

// Broadcaster manages WebSocket client connections and fan-out.
type Broadcaster struct {
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan Message
	mu         sync.RWMutex
	logger     *slog.Logger
}

// NewBroadcaster creates a new broadcaster.
func NewBroadcaster(logger *slog.Logger) *Broadcaster {
	return &Broadcaster{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		broadcast:  make(chan Message, 256),
		logger:     logger,
	}
}

// Run starts the broadcaster event loop. Should be called in a goroutine.
func (b *Broadcaster) Run() {
	for {
		select {
		case client := <-b.register:
			b.mu.Lock()
			b.clients[client] = true
			b.mu.Unlock()
			b.logger.Debug("client connected", "ip", client.ip, "scopes", client.scopes, "total", b.Count())

		case client := <-b.unregister:
			b.mu.Lock()
			if _, ok := b.clients[client]; ok {
				delete(b.clients, client)
				close(client.send)
			}
			b.mu.Unlock()
			b.logger.Debug("client disconnected", "ip", client.ip, "total", b.Count())

		case msg := <-b.broadcast:
			data, err := json.Marshal(msg)
			if err != nil {
				b.logger.Error("failed to marshal broadcast message", "error", err)
				continue
			}

			b.mu.RLock()
			for client := range b.clients {
				if !clientAllowed(client, msg.Type) {
					continue
				}
				select {
				case client.send <- data:
				default:
					// Client too slow, disconnect
					b.mu.RUnlock()
					b.mu.Lock()
					delete(b.clients, client)
					close(client.send)
					b.mu.Unlock()
					b.mu.RLock()
				}
			}
			b.mu.RUnlock()
		}
	}
}

// Register adds a client to the broadcaster.
func (b *Broadcaster) Register(client *Client) {
	b.register <- client
}

// Unregister removes a client from the broadcaster.
func (b *Broadcaster) Unregister(client *Client) {
	b.unregister <- client
}

// Send broadcasts a message to all matching clients.
func (b *Broadcaster) Send(msgType string, data any) {
	b.broadcast <- Message{Type: msgType, Data: data}
}

// Count returns the number of connected clients.
func (b *Broadcaster) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// scopeEvents maps stream scopes to the event types they cover.
// Mirrors backend SCOPE_EVENTS in ws/server.ts.
var scopeEvents = map[string][]string{
	"signals":     {"signal"},
	"markets":     {"market", "market_correlation"},
	"aircraft":    {"aircraft"},
	"vessels":     {"vessel"},
	"alerts":      {"alert"},
	"social":      {"x_post", "tg_message"},
	"earthquakes": {"signal"},
	"airspace":    {"signal"},
	"cyber":       {"signal"},
}

// clientAllowed checks if a client's scopes permit receiving a given event type.
func clientAllowed(c *Client, eventType string) bool {
	// No scopes = public client, receives everything
	if len(c.scopes) == 0 {
		return true
	}

	for _, scope := range c.scopes {
		events, ok := scopeEvents[scope]
		if !ok {
			continue
		}
		for _, e := range events {
			if e == eventType {
				return true
			}
		}
	}
	return false
}
