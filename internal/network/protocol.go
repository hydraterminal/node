package network

import "time"

// SubmissionBatch is the payload sent to POST /nodes/submit.
type SubmissionBatch struct {
	NodeID      string           `json:"nodeId"`
	BatchID     string           `json:"batchId"`
	SubmittedAt time.Time        `json:"submittedAt"`
	Signals     []SubmitSignal   `json:"signals"`
	Aircraft    []any            `json:"aircraft,omitempty"`
	Vessels     []any            `json:"vessels,omitempty"`
	Markets     []any            `json:"markets,omitempty"`
	Signature   string           `json:"signature"`
}

// SubmitSignal is a signal with its hash for cross-validation.
type SubmitSignal struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Severity    string         `json:"severity"`
	Category    string         `json:"category"`
	Source      string         `json:"source"`
	SourceRef   string         `json:"sourceRef"`
	Region      string         `json:"region,omitempty"`
	Countries   []string       `json:"countries,omitempty"`
	Latitude    *float64       `json:"latitude,omitempty"`
	Longitude   *float64       `json:"longitude,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CollectedAt time.Time      `json:"collectedAt"`
	Hash        string         `json:"hash"`
}

// SubmissionReceipt is returned by the backend after a submission.
type SubmissionReceipt struct {
	BatchID          string `json:"batchId"`
	Accepted         int    `json:"accepted"`
	Rejected         int    `json:"rejected"`
	Duplicate        int    `json:"duplicate"`
	ReceiptSignature string `json:"receiptSignature"`
}

// HeartbeatPayload is sent to POST /nodes/heartbeat.
type HeartbeatPayload struct {
	NodeID        string           `json:"nodeId"`
	Timestamp     time.Time        `json:"timestamp"`
	Status        string           `json:"status"`
	Version       string           `json:"version,omitempty"`
	ActiveSources []string         `json:"activeSources"`
	Metrics       HeartbeatMetrics `json:"metrics"`
}

// HeartbeatMetrics are operational metrics reported with each heartbeat.
type HeartbeatMetrics struct {
	SignalsSubmitted int     `json:"signalsSubmitted"`
	UptimeSeconds    int64   `json:"uptimeSeconds"`
	MemoryMB         int     `json:"memoryMB"`
	CPUPercent       float64 `json:"cpuPercent"`
	NetworkTxBytes   uint64  `json:"networkTxBytes"`
	NetworkRxBytes   uint64  `json:"networkRxBytes"`
}

// ChallengeResponse from POST /nodes/auth/challenge.
type ChallengeResponse struct {
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// VerifyResponse from POST /nodes/auth/verify.
type VerifyResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	NodeID    string    `json:"nodeId"`
}

// LogBatch is sent to POST /nodes/logs.
type LogBatch struct {
	NodeID  string     `json:"nodeId"`
	Entries []LogEntry `json:"entries"`
}

// LogEntry is a single log line sent to the backend.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}
