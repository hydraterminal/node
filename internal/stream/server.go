package stream

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/hydraterminal/node/internal/auth"
	"github.com/hydraterminal/node/internal/store"
)

const (
	pingInterval          = 15 * time.Second
	writeWait             = 10 * time.Second
	pongWait              = 30 * time.Second
	maxConnectionsPerIP   = 10
	maxMessageSize        = 1024 // Clients don't send meaningful data
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Server handles WebSocket connections for the node's streaming endpoints.
type Server struct {
	broadcaster *Broadcaster
	store       store.Store
	keyValidator *auth.KeyValidator
	logger      *slog.Logger

	// Track connections per IP for rate limiting
	connsByIP sync.Map
}

// NewServer creates a new WebSocket server.
func NewServer(broadcaster *Broadcaster, store store.Store, keyValidator *auth.KeyValidator, logger *slog.Logger) *Server {
	return &Server{
		broadcaster:  broadcaster,
		store:        store,
		keyValidator: keyValidator,
		logger:       logger,
	}
}

// HandleLiveStream handles the /stream/live WebSocket endpoint (public, rate-limited).
func (s *Server) HandleLiveStream(w http.ResponseWriter, r *http.Request) {
	ip := getClientIP(r)

	// Check connection limit per IP
	if s.getIPConnCount(ip) >= maxConnectionsPerIP {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("ws upgrade failed", "error", err)
		return
	}

	client := &Client{
		conn:   conn,
		send:   make(chan []byte, 256),
		scopes: nil, // nil = public, receives all events
		ip:     ip,
	}

	s.incrIPConn(ip)
	s.broadcaster.Register(client)

	go s.writePump(client, ip)
	go s.readPump(client, ip)

	// Send initial snapshot
	s.sendSnapshot(client)
}

// HandleV1Stream handles the /v1/stream WebSocket endpoint (API key required, scoped).
func (s *Server) HandleV1Stream(w http.ResponseWriter, r *http.Request) {
	// Extract API key
	apiKey := r.Header.Get("x-api-key")
	if apiKey == "" {
		apiKey = r.URL.Query().Get("apiKey")
	}
	if apiKey == "" {
		http.Error(w, "API key required", http.StatusUnauthorized)
		return
	}

	// Validate API key against backend
	result, err := s.keyValidator.Validate(apiKey)
	if err != nil || !result.Valid {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
		return
	}

	ip := getClientIP(r)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("ws upgrade failed", "error", err)
		return
	}

	client := &Client{
		conn:   conn,
		send:   make(chan []byte, 256),
		scopes: result.Scopes,
		ip:     ip,
	}

	s.incrIPConn(ip)
	s.broadcaster.Register(client)

	go s.writePump(client, ip)
	go s.readPump(client, ip)

	// Send scoped snapshot
	s.sendSnapshot(client)
}

func (s *Server) writePump(client *Client, ip string) {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		client.conn.Close()
		s.broadcaster.Unregister(client)
		s.decrIPConn(ip)
	}()

	for {
		select {
		case message, ok := <-client.send:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := client.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			client.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) readPump(client *Client, ip string) {
	defer func() {
		s.broadcaster.Unregister(client)
		client.conn.Close()
		s.decrIPConn(ip)
	}()

	client.conn.SetReadLimit(maxMessageSize)
	client.conn.SetReadDeadline(time.Now().Add(pongWait))
	client.conn.SetPongHandler(func(string) error {
		client.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, _, err := client.conn.ReadMessage()
		if err != nil {
			return
		}
	}
}

// sendSnapshot sends the initial state to a newly connected client.
func (s *Server) sendSnapshot(client *Client) {
	// Send connected message
	s.sendToClient(client, "connected", map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339),
	})

	if s.store == nil {
		return
	}

	// Send aircraft snapshot
	if clientAllowed(client, "aircraft") {
		if aircraft, err := s.store.GetAircraft(); err == nil && len(aircraft) > 0 {
			s.sendToClient(client, "aircraft", map[string]any{
				"positions": aircraft,
				"count":     len(aircraft),
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	// Send vessel snapshot
	if clientAllowed(client, "vessel") {
		if vessels, err := s.store.GetVessels(); err == nil && len(vessels) > 0 {
			s.sendToClient(client, "vessel", map[string]any{
				"positions": vessels,
				"count":     len(vessels),
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	// Send signal snapshot
	if clientAllowed(client, "signal") {
		if signals, err := s.store.GetSignals(store.SignalQuery{Limit: 50}); err == nil && len(signals) > 0 {
			s.sendToClient(client, "signal", map[string]any{
				"signals": signals,
			})
		}
	}

	// Send alert snapshot
	if clientAllowed(client, "alert") {
		if alerts, err := s.store.GetAlerts(); err == nil && len(alerts) > 0 {
			s.sendToClient(client, "alert", map[string]any{
				"alerts": alerts,
				"count":  len(alerts),
			})
		}
	}

	// Send market snapshot
	if clientAllowed(client, "market") {
		if markets, err := s.store.GetMarkets(); err == nil && len(markets) > 0 {
			s.sendToClient(client, "market", map[string]any{
				"markets": markets,
			})
		}
	}

	// Send online count
	s.sendToClient(client, "online_count", map[string]any{
		"count": s.broadcaster.Count(),
	})
}

func (s *Server) sendToClient(client *Client, msgType string, data any) {
	msg := Message{Type: msgType, Data: data}
	bytes, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case client.send <- bytes:
	default:
	}
}

func (s *Server) getIPConnCount(ip string) int {
	val, ok := s.connsByIP.Load(ip)
	if !ok {
		return 0
	}
	return val.(int)
}

func (s *Server) incrIPConn(ip string) {
	val, _ := s.connsByIP.LoadOrStore(ip, 0)
	s.connsByIP.Store(ip, val.(int)+1)
}

func (s *Server) decrIPConn(ip string) {
	val, ok := s.connsByIP.Load(ip)
	if !ok {
		return
	}
	n := val.(int) - 1
	if n <= 0 {
		s.connsByIP.Delete(ip)
	} else {
		s.connsByIP.Store(ip, n)
	}
}

func getClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return forwarded
	}
	return r.RemoteAddr
}
