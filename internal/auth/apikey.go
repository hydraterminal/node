package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/hydraterminal/node/internal/store"
)

// ValidationResult holds the result of an API key validation.
type ValidationResult struct {
	Valid     bool     `json:"valid"`
	Scopes   []string `json:"scopes"`
	Tier     string   `json:"tier"`
	AppID    string   `json:"appId"`
	RateLimit int     `json:"rateLimit"`
}

type cacheEntry struct {
	result    ValidationResult
	expiresAt time.Time
}

// KeyValidator validates API keys against the Hydra backend with local caching.
type KeyValidator struct {
	backendURL string
	cache      map[string]cacheEntry
	cacheTTL   time.Duration
	mu         sync.RWMutex
	client     *http.Client
	logger     *slog.Logger

	// Local operator keys (configured in node.yaml, always valid)
	operatorKeys map[string]ValidationResult

	// Local SQLite store for node-managed API keys
	localStore *store.SQLiteStore
}

// NewKeyValidator creates a new API key validator.
func NewKeyValidator(backendURL string, logger *slog.Logger) *KeyValidator {
	return &KeyValidator{
		backendURL:   backendURL,
		cache:        make(map[string]cacheEntry),
		cacheTTL:     5 * time.Minute,
		client:       &http.Client{Timeout: 10 * time.Second},
		logger:       logger,
		operatorKeys: make(map[string]ValidationResult),
	}
}

// SetLocalStore sets the local SQLite store for validating node-managed API keys.
func (v *KeyValidator) SetLocalStore(s *store.SQLiteStore) {
	v.localStore = s
}

// AddOperatorKey adds a locally-configured operator key that bypasses backend validation.
func (v *KeyValidator) AddOperatorKey(key string, scopes []string) {
	v.operatorKeys[key] = ValidationResult{
		Valid:     true,
		Scopes:   scopes,
		Tier:     "OPERATOR",
		RateLimit: 1000,
	}
}

// Validate checks an API key. First checks local operator keys, then local
// SQLite store (node-managed keys), then cache, then calls the backend.
func (v *KeyValidator) Validate(apiKey string) (*ValidationResult, error) {
	// Check operator keys first
	if result, ok := v.operatorKeys[apiKey]; ok {
		return &result, nil
	}

	// Check local SQLite store (node-managed keys)
	if v.localStore != nil {
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := hex.EncodeToString(hash[:])
		localResult, err := v.localStore.ValidateApiKey(keyHash)
		if err != nil {
			v.logger.Warn("local key validation error", "error", err)
		} else if localResult.Valid {
			return &ValidationResult{
				Valid:     true,
				Scopes:   localResult.Scopes,
				Tier:     "LOCAL",
				RateLimit: 1000,
			}, nil
		}
	}

	// Check cache
	v.mu.RLock()
	if entry, ok := v.cache[apiKey]; ok && time.Now().Before(entry.expiresAt) {
		v.mu.RUnlock()
		return &entry.result, nil
	}
	v.mu.RUnlock()

	// Call backend
	result, err := v.validateRemote(apiKey)
	if err != nil {
		// On backend failure, check if we have a stale cache entry
		v.mu.RLock()
		if entry, ok := v.cache[apiKey]; ok {
			v.mu.RUnlock()
			v.logger.Warn("using stale cache for API key validation", "error", err)
			return &entry.result, nil
		}
		v.mu.RUnlock()
		return nil, err
	}

	// Cache the result
	v.mu.Lock()
	v.cache[apiKey] = cacheEntry{
		result:    *result,
		expiresAt: time.Now().Add(v.cacheTTL),
	}
	v.mu.Unlock()

	return result, nil
}

func (v *KeyValidator) validateRemote(apiKey string) (*ValidationResult, error) {
	body, _ := json.Marshal(map[string]string{"apiKey": apiKey})
	url := fmt.Sprintf("%s/nodes/validate-key", v.backendURL)

	resp, err := v.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("backend request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &ValidationResult{Valid: false}, nil
	}

	var result ValidationResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}

// CleanCache removes expired entries from the cache.
func (v *KeyValidator) CleanCache() {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	for key, entry := range v.cache {
		if now.After(entry.expiresAt) {
			delete(v.cache, key)
		}
	}
}
