package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/hydraterminal/node/internal/auth"
	"github.com/hydraterminal/node/internal/control"
	"github.com/hydraterminal/node/internal/store"
	"github.com/hydraterminal/node/internal/stream"
)

// Server is the client-facing HTTP + WebSocket API server.
type Server struct {
	mux          *http.ServeMux
	store        store.Store
	wsServer     *stream.Server
	keyValidator *auth.KeyValidator
	logger       *slog.Logger
	startTime    time.Time
	version      string
}

// NewServer creates a new API server.
func NewServer(st store.Store, broadcaster *stream.Broadcaster, keyValidator *auth.KeyValidator, logger *slog.Logger) *Server {
	s := &Server{
		mux:          http.NewServeMux(),
		store:        st,
		wsServer:     stream.NewServer(broadcaster, st, keyValidator, logger),
		keyValidator: keyValidator,
		logger:       logger,
		startTime:    time.Now(),
	}

	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// WebSocket endpoints
	s.mux.HandleFunc("/stream/live", s.wsServer.HandleLiveStream)
	s.mux.HandleFunc("/v1/stream", s.wsServer.HandleV1Stream)

	// Public REST endpoints (rate-limited, no auth)
	s.mux.HandleFunc("GET /public/signals", s.handlePublicSignals)
	s.mux.HandleFunc("GET /public/aircraft", s.handlePublicAircraft)
	s.mux.HandleFunc("GET /public/vessels", s.handlePublicVessels)
	s.mux.HandleFunc("GET /public/markets", s.handlePublicMarkets)

	// Authenticated REST endpoints
	s.mux.HandleFunc("GET /v1/signals", s.requireAuth(s.handleSignals))
	s.mux.HandleFunc("GET /v1/aircraft", s.requireAuth(s.handleAircraft))
	s.mux.HandleFunc("GET /v1/vessels", s.requireAuth(s.handleVessels))
	s.mux.HandleFunc("GET /v1/markets", s.requireAuth(s.handleMarkets))

	// Health check
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /status", s.handleStatus)
}

// Handler returns the HTTP handler for this server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// requireAuth is middleware that validates API keys.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("x-api-key")
		if apiKey == "" {
			apiKey = r.URL.Query().Get("apiKey")
		}
		if apiKey == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "API key required"})
			return
		}

		result, err := s.keyValidator.Validate(apiKey)
		if err != nil {
			s.logger.Error("key validation error", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "validation service unavailable"})
			return
		}
		if !result.Valid {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API key"})
			return
		}

		next(w, r)
	}
}

// Public handlers (thinned data, rate-limited)

func (s *Server) handlePublicSignals(w http.ResponseWriter, r *http.Request) {
	signals, err := s.store.GetSignals(store.SignalQuery{Limit: 50})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, signals)
}

func (s *Server) handlePublicAircraft(w http.ResponseWriter, r *http.Request) {
	aircraft, err := s.store.GetAircraft()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Thin to every 3rd aircraft for public
	thinned := make([]any, 0)
	for i, ac := range aircraft {
		if i%3 == 0 {
			thinned = append(thinned, ac)
		}
	}
	writeJSON(w, http.StatusOK, thinned)
}

func (s *Server) handlePublicVessels(w http.ResponseWriter, r *http.Request) {
	vessels, err := s.store.GetVessels()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, vessels)
}

func (s *Server) handlePublicMarkets(w http.ResponseWriter, r *http.Request) {
	markets, err := s.store.GetMarkets()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, markets)
}

// Authenticated handlers (full data)

func (s *Server) handleSignals(w http.ResponseWriter, r *http.Request) {
	q := store.SignalQuery{
		Category: r.URL.Query().Get("category"),
		Severity: r.URL.Query().Get("severity"),
		Source:   r.URL.Query().Get("source"),
		Limit:    200,
	}
	signals, err := s.store.GetSignals(q)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, signals)
}

func (s *Server) handleAircraft(w http.ResponseWriter, r *http.Request) {
	aircraft, err := s.store.GetAircraft()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, aircraft)
}

func (s *Server) handleVessels(w http.ResponseWriter, r *http.Request) {
	vessels, err := s.store.GetVessels()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, vessels)
}

func (s *Server) handleMarkets(w http.ResponseWriter, r *http.Request) {
	markets, err := s.store.GetMarkets()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, markets)
}

// Health/Status

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"version":   s.version,
		"uptime":    time.Since(s.startTime).String(),
		"startedAt": s.startTime.UTC().Format(time.RFC3339),
	})
}

// SetVersion sets the version string reported in the status endpoint.
func (s *Server) SetVersion(v string) { s.version = v }

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// SetSourceStatus allows injecting source status into the /status endpoint.
func (s *Server) SetSourceStatus(fn func() any) {
	s.mux.HandleFunc("GET /status/sources", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, fn())
	})
}

// MountControl registers control plane routes onto this server's mux.
func (s *Server) MountControl(ctrl *control.Server) {
	ctrl.Register(s.mux)
}
