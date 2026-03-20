package network

import (
	"log/slog"
	"net/http"
	"time"
)

// NodeAuth manages authentication with the Hydra backend using a shared secret.
// The node ID and secret are generated on Platform → Nodes → Create Node.
type NodeAuth struct {
	backendURL string
	nodeID     string
	secret     string
	client     *http.Client
	logger     *slog.Logger
}

// NewNodeAuth creates a new node authenticator.
func NewNodeAuth(backendURL, nodeID, secret string, logger *slog.Logger) *NodeAuth {
	return &NodeAuth{
		backendURL: backendURL,
		nodeID:     nodeID,
		secret:     secret,
		client:     &http.Client{Timeout: 15 * time.Second},
		logger:     logger,
	}
}

// NodeID returns the node ID.
func (a *NodeAuth) NodeID() string {
	return a.nodeID
}

// BackendURL returns the configured backend URL.
func (a *NodeAuth) BackendURL() string {
	return a.backendURL
}

// Do executes a request against the backend with node auth headers.
func (a *NodeAuth) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("x-node-id", a.nodeID)
	req.Header.Set("x-node-secret", a.secret)
	req.Header.Set("Content-Type", "application/json")
	return a.client.Do(req)
}
