package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hydraterminal/node/internal/source"
)

// SQLiteStore implements Store using a local SQLite database.
type SQLiteStore struct {
	db             *sql.DB
	retentionHours int
}

// NewSQLiteStore creates a new SQLite-backed store.
func NewSQLiteStore(dataDir string, retentionHours int) (*SQLiteStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "hydra-node.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	return &SQLiteStore{
		db:             db,
		retentionHours: retentionHours,
	}, nil
}

// Init creates the database tables.
func (s *SQLiteStore) Init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS signals (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		description TEXT,
		severity TEXT NOT NULL,
		category TEXT NOT NULL,
		source TEXT NOT NULL,
		source_ref TEXT NOT NULL,
		region TEXT,
		countries TEXT,
		latitude REAL,
		longitude REAL,
		metadata TEXT,
		expires_at TEXT,
		collected_at TEXT NOT NULL,
		submitted INTEGER DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_signals_collected ON signals(collected_at);
	CREATE INDEX IF NOT EXISTS idx_signals_category ON signals(category);
	CREATE INDEX IF NOT EXISTS idx_signals_source_ref ON signals(source_ref);
	CREATE INDEX IF NOT EXISTS idx_signals_submitted ON signals(submitted);

	CREATE TABLE IF NOT EXISTS aircraft (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hex TEXT NOT NULL,
		callsign TEXT,
		type TEXT,
		registration TEXT,
		operator TEXT,
		category TEXT,
		latitude REAL NOT NULL,
		longitude REAL NOT NULL,
		altitude REAL,
		speed REAL,
		track REAL,
		vertical_rate REAL,
		squawk TEXT,
		flag TEXT,
		collected_at TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_aircraft_collected ON aircraft(collected_at);
	CREATE INDEX IF NOT EXISTS idx_aircraft_hex ON aircraft(hex);

	CREATE TABLE IF NOT EXISTS vessels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		mmsi TEXT NOT NULL,
		name TEXT,
		call_sign TEXT,
		imo TEXT,
		ship_type INTEGER,
		category TEXT,
		flag TEXT,
		destination TEXT,
		latitude REAL NOT NULL,
		longitude REAL NOT NULL,
		speed REAL,
		course REAL,
		heading REAL,
		nav_status INTEGER,
		collected_at TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_vessels_collected ON vessels(collected_at);
	CREATE INDEX IF NOT EXISTS idx_vessels_mmsi ON vessels(mmsi);

	CREATE TABLE IF NOT EXISTS alerts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		alert_id TEXT NOT NULL,
		alert_type TEXT,
		title TEXT,
		description TEXT,
		severity TEXT,
		cities TEXT,
		latitude REAL,
		longitude REAL,
		expires_at TEXT,
		collected_at TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_alerts_collected ON alerts(collected_at);

	CREATE TABLE IF NOT EXISTS markets (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		market_id TEXT NOT NULL,
		question TEXT,
		description TEXT,
		outcomes TEXT,
		prices TEXT,
		volume REAL,
		liquidity REAL,
		end_date TEXT,
		tags TEXT,
		collected_at TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_markets_collected ON markets(collected_at);

	CREATE TABLE IF NOT EXISTS posts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		external_id TEXT NOT NULL,
		platform TEXT NOT NULL,
		author TEXT,
		text TEXT,
		url TEXT,
		timestamp TEXT,
		likes INTEGER,
		retweets INTEGER,
		views INTEGER,
		photos TEXT,
		collected_at TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_posts_collected ON posts(collected_at);
	CREATE INDEX IF NOT EXISTS idx_posts_platform ON posts(platform);

	CREATE TABLE IF NOT EXISTS api_keys (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		key_prefix TEXT NOT NULL,
		key_hash TEXT NOT NULL UNIQUE,
		scopes TEXT NOT NULL DEFAULT '["read"]',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		last_used_at TEXT,
		revoked INTEGER NOT NULL DEFAULT 0
	);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// InsertBatch writes a collected batch to the database.
func (s *SQLiteStore) InsertBatch(batch source.CollectedBatch) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := batch.CollectedAt.UTC().Format(time.RFC3339)

	// Insert signals
	for _, sig := range batch.Signals {
		countries, _ := json.Marshal(sig.Countries)
		metadata, _ := json.Marshal(sig.Metadata)
		var expiresAt *string
		if sig.ExpiresAt != nil {
			t := sig.ExpiresAt.UTC().Format(time.RFC3339)
			expiresAt = &t
		}
		_, err := tx.Exec(`INSERT INTO signals (title, description, severity, category, source, source_ref, region, countries, latitude, longitude, metadata, expires_at, collected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sig.Title, sig.Description, sig.Severity, sig.Category, sig.Source,
			sig.SourceRef, sig.Region, string(countries), sig.Latitude, sig.Longitude,
			string(metadata), expiresAt, now)
		if err != nil {
			return fmt.Errorf("insert signal: %w", err)
		}
	}

	// Insert aircraft
	for _, ac := range batch.Aircraft {
		_, err := tx.Exec(`INSERT INTO aircraft (hex, callsign, type, registration, operator, category, latitude, longitude, altitude, speed, track, vertical_rate, squawk, flag, collected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ac.Hex, ac.Callsign, ac.Type, ac.Registration, ac.Operator, ac.Category,
			ac.Latitude, ac.Longitude, ac.Altitude, ac.Speed, ac.Track, ac.VerticalRate,
			ac.Squawk, ac.Flag, now)
		if err != nil {
			return fmt.Errorf("insert aircraft: %w", err)
		}
	}

	// Insert vessels
	for _, v := range batch.Vessels {
		_, err := tx.Exec(`INSERT INTO vessels (mmsi, name, call_sign, imo, ship_type, category, flag, destination, latitude, longitude, speed, course, heading, nav_status, collected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.MMSI, v.Name, v.CallSign, v.IMO, v.ShipType, v.Category, v.Flag,
			v.Destination, v.Latitude, v.Longitude, v.Speed, v.Course, v.Heading,
			v.NavStatus, now)
		if err != nil {
			return fmt.Errorf("insert vessel: %w", err)
		}
	}

	// Insert alerts
	for _, a := range batch.Alerts {
		cities, _ := json.Marshal(a.Cities)
		_, err := tx.Exec(`INSERT INTO alerts (alert_id, alert_type, title, description, severity, cities, latitude, longitude, expires_at, collected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.AlertType, a.Title, a.Description, a.Severity,
			string(cities), a.Latitude, a.Longitude,
			a.ExpiresAt.UTC().Format(time.RFC3339), now)
		if err != nil {
			return fmt.Errorf("insert alert: %w", err)
		}
	}

	// Insert markets
	for _, m := range batch.Markets {
		outcomes, _ := json.Marshal(m.Outcomes)
		prices, _ := json.Marshal(m.Prices)
		tags, _ := json.Marshal(m.Tags)
		_, err := tx.Exec(`INSERT INTO markets (market_id, question, description, outcomes, prices, volume, liquidity, end_date, tags, collected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.MarketID, m.Question, m.Description, string(outcomes), string(prices),
			m.Volume, m.Liquidity, m.EndDate, string(tags), now)
		if err != nil {
			return fmt.Errorf("insert market: %w", err)
		}
	}

	// Insert posts
	for _, p := range batch.Posts {
		photos, _ := json.Marshal(p.Photos)
		_, err := tx.Exec(`INSERT INTO posts (external_id, platform, author, text, url, timestamp, likes, retweets, views, photos, collected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, p.Platform, p.Author, p.Text, p.URL,
			p.Timestamp.UTC().Format(time.RFC3339), p.Likes, p.Retweets,
			p.Views, string(photos), now)
		if err != nil {
			return fmt.Errorf("insert post: %w", err)
		}
	}

	return tx.Commit()
}

// GetSignals retrieves recent signals from the database.
func (s *SQLiteStore) GetSignals(opts SignalQuery) ([]source.NormalizedSignal, error) {
	query := `SELECT title, description, severity, category, source, source_ref, region, countries, latitude, longitude, metadata, expires_at, collected_at FROM signals WHERE 1=1`
	args := []any{}

	if opts.Category != "" {
		query += " AND category = ?"
		args = append(args, opts.Category)
	}
	if opts.Severity != "" {
		query += " AND severity = ?"
		args = append(args, opts.Severity)
	}
	if opts.Source != "" {
		query += " AND source = ?"
		args = append(args, opts.Source)
	}

	query += " ORDER BY collected_at DESC"

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT %d", limit)
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var signals []source.NormalizedSignal
	for rows.Next() {
		var sig source.NormalizedSignal
		var countries, metadata, expiresAt, collectedAt sql.NullString
		var lat, lng sql.NullFloat64

		err := rows.Scan(&sig.Title, &sig.Description, &sig.Severity, &sig.Category,
			&sig.Source, &sig.SourceRef, &sig.Region, &countries, &lat, &lng,
			&metadata, &expiresAt, &collectedAt)
		if err != nil {
			return nil, err
		}

		if lat.Valid {
			sig.Latitude = &lat.Float64
		}
		if lng.Valid {
			sig.Longitude = &lng.Float64
		}
		if countries.Valid {
			json.Unmarshal([]byte(countries.String), &sig.Countries)
		}
		if metadata.Valid {
			json.Unmarshal([]byte(metadata.String), &sig.Metadata)
		}
		if collectedAt.Valid {
			sig.CollectedAt, _ = time.Parse(time.RFC3339, collectedAt.String)
		}
		if expiresAt.Valid {
			t, _ := time.Parse(time.RFC3339, expiresAt.String)
			sig.ExpiresAt = &t
		}

		signals = append(signals, sig)
	}
	return signals, nil
}

// GetAircraft retrieves the latest aircraft positions (deduped by hex).
func (s *SQLiteStore) GetAircraft() ([]source.NormalizedAircraft, error) {
	rows, err := s.db.Query(`SELECT hex, callsign, type, registration, operator, category, latitude, longitude, altitude, speed, track, vertical_rate, squawk, flag
		FROM aircraft WHERE id IN (SELECT MAX(id) FROM aircraft GROUP BY hex) ORDER BY collected_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var aircraft []source.NormalizedAircraft
	for rows.Next() {
		var ac source.NormalizedAircraft
		err := rows.Scan(&ac.Hex, &ac.Callsign, &ac.Type, &ac.Registration,
			&ac.Operator, &ac.Category, &ac.Latitude, &ac.Longitude,
			&ac.Altitude, &ac.Speed, &ac.Track, &ac.VerticalRate,
			&ac.Squawk, &ac.Flag)
		if err != nil {
			return nil, err
		}
		aircraft = append(aircraft, ac)
	}
	return aircraft, nil
}

// GetVessels retrieves the latest vessel positions (deduped by MMSI).
func (s *SQLiteStore) GetVessels() ([]source.NormalizedVessel, error) {
	rows, err := s.db.Query(`SELECT mmsi, name, call_sign, imo, ship_type, category, flag, destination, latitude, longitude, speed, course, heading, nav_status
		FROM vessels WHERE id IN (SELECT MAX(id) FROM vessels GROUP BY mmsi) ORDER BY collected_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vessels []source.NormalizedVessel
	for rows.Next() {
		var v source.NormalizedVessel
		err := rows.Scan(&v.MMSI, &v.Name, &v.CallSign, &v.IMO, &v.ShipType,
			&v.Category, &v.Flag, &v.Destination, &v.Latitude, &v.Longitude,
			&v.Speed, &v.Course, &v.Heading, &v.NavStatus)
		if err != nil {
			return nil, err
		}
		vessels = append(vessels, v)
	}
	return vessels, nil
}

// GetMarkets retrieves recent market data.
func (s *SQLiteStore) GetMarkets() ([]source.NormalizedMarket, error) {
	rows, err := s.db.Query(`SELECT market_id, question, description, outcomes, prices, volume, liquidity, end_date, tags
		FROM markets WHERE id IN (SELECT MAX(id) FROM markets GROUP BY market_id) ORDER BY collected_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var markets []source.NormalizedMarket
	for rows.Next() {
		var m source.NormalizedMarket
		var outcomes, prices, tags string
		err := rows.Scan(&m.MarketID, &m.Question, &m.Description, &outcomes,
			&prices, &m.Volume, &m.Liquidity, &m.EndDate, &tags)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(outcomes), &m.Outcomes)
		json.Unmarshal([]byte(prices), &m.Prices)
		json.Unmarshal([]byte(tags), &m.Tags)
		markets = append(markets, m)
	}
	return markets, nil
}

// GetPosts retrieves recent posts, optionally filtered by platform.
func (s *SQLiteStore) GetPosts(platform string) ([]source.NormalizedPost, error) {
	query := `SELECT external_id, platform, author, text, url, timestamp, likes, retweets, views, photos FROM posts`
	args := []any{}
	if platform != "" {
		query += " WHERE platform = ?"
		args = append(args, platform)
	}
	query += " ORDER BY collected_at DESC LIMIT 100"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []source.NormalizedPost
	for rows.Next() {
		var p source.NormalizedPost
		var ts, photos string
		err := rows.Scan(&p.ID, &p.Platform, &p.Author, &p.Text, &p.URL,
			&ts, &p.Likes, &p.Retweets, &p.Views, &photos)
		if err != nil {
			return nil, err
		}
		p.Timestamp, _ = time.Parse(time.RFC3339, ts)
		json.Unmarshal([]byte(photos), &p.Photos)
		posts = append(posts, p)
	}
	return posts, nil
}

// GetAlerts returns currently active alerts.
func (s *SQLiteStore) GetAlerts() ([]source.NormalizedAlert, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.Query(`SELECT alert_id, alert_type, title, description, severity, cities, latitude, longitude, expires_at
		FROM alerts WHERE expires_at > ? ORDER BY collected_at DESC LIMIT 100`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []source.NormalizedAlert
	for rows.Next() {
		var a source.NormalizedAlert
		var cities, expiresAt string
		err := rows.Scan(&a.ID, &a.AlertType, &a.Title, &a.Description, &a.Severity,
			&cities, &a.Latitude, &a.Longitude, &expiresAt)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(cities), &a.Cities)
		a.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
		alerts = append(alerts, a)
	}
	return alerts, nil
}

// GetPendingSubmission returns batches not yet submitted to the network.
func (s *SQLiteStore) GetPendingSubmission(limit int) ([]source.CollectedBatch, error) {
	// For now, return unsubmitted signals as a batch
	rows, err := s.db.Query(`SELECT title, description, severity, category, source, source_ref, region, countries, latitude, longitude, metadata, collected_at
		FROM signals WHERE submitted = 0 ORDER BY collected_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	batch := source.CollectedBatch{
		Source:      "pending",
		CollectedAt: time.Now(),
	}

	for rows.Next() {
		var sig source.NormalizedSignal
		var countries, metadata, collectedAt sql.NullString
		var lat, lng sql.NullFloat64

		err := rows.Scan(&sig.Title, &sig.Description, &sig.Severity, &sig.Category,
			&sig.Source, &sig.SourceRef, &sig.Region, &countries, &lat, &lng,
			&metadata, &collectedAt)
		if err != nil {
			return nil, err
		}

		if lat.Valid {
			sig.Latitude = &lat.Float64
		}
		if lng.Valid {
			sig.Longitude = &lng.Float64
		}
		if countries.Valid {
			json.Unmarshal([]byte(countries.String), &sig.Countries)
		}
		if metadata.Valid {
			json.Unmarshal([]byte(metadata.String), &sig.Metadata)
		}
		if collectedAt.Valid {
			sig.CollectedAt, _ = time.Parse(time.RFC3339, collectedAt.String)
		}

		batch.Signals = append(batch.Signals, sig)
	}

	if len(batch.Signals) == 0 {
		return nil, nil
	}
	return []source.CollectedBatch{batch}, nil
}

// MarkSubmitted marks signals as submitted.
func (s *SQLiteStore) MarkSubmitted(batchIDs []string) error {
	// Mark all unsubmitted signals as submitted
	_, err := s.db.Exec(`UPDATE signals SET submitted = 1 WHERE submitted = 0`)
	return err
}

// Prune deletes data older than the retention period.
func (s *SQLiteStore) Prune() error {
	cutoff := time.Now().Add(-time.Duration(s.retentionHours) * time.Hour).UTC().Format(time.RFC3339)

	tables := []string{"signals", "aircraft", "vessels", "alerts", "markets", "posts"}
	for _, table := range tables {
		_, err := s.db.Exec(fmt.Sprintf("DELETE FROM %s WHERE collected_at < ?", table), cutoff)
		if err != nil {
			return fmt.Errorf("prune %s: %w", table, err)
		}
	}
	return nil
}

// ApiKeyInfo represents an API key's public metadata (never includes the hash).
type ApiKeyInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	KeyPrefix  string   `json:"keyPrefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"createdAt"`
	LastUsedAt *string  `json:"lastUsedAt"`
	Revoked    bool     `json:"revoked"`
}

// ApiKeyValidation is returned when checking a key hash against the store.
type ApiKeyValidation struct {
	Valid  bool     `json:"valid"`
	Scopes []string `json:"scopes"`
}

// CreateApiKey inserts a new API key record.
func (s *SQLiteStore) CreateApiKey(id, name, keyPrefix, keyHash string, scopes []string) error {
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return fmt.Errorf("marshal scopes: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO api_keys (id, name, key_prefix, key_hash, scopes) VALUES (?, ?, ?, ?, ?)`,
		id, name, keyPrefix, keyHash, string(scopesJSON),
	)
	return err
}

// ListApiKeys returns all API keys (without hashes), sorted by created_at desc.
func (s *SQLiteStore) ListApiKeys() ([]ApiKeyInfo, error) {
	rows, err := s.db.Query(`SELECT id, name, key_prefix, scopes, created_at, last_used_at, revoked FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []ApiKeyInfo
	for rows.Next() {
		var k ApiKeyInfo
		var scopesStr string
		var lastUsed sql.NullString
		var revoked int

		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &scopesStr, &k.CreatedAt, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(scopesStr), &k.Scopes)
		if lastUsed.Valid {
			k.LastUsedAt = &lastUsed.String
		}
		k.Revoked = revoked != 0
		keys = append(keys, k)
	}
	return keys, nil
}

// RevokeApiKey marks an API key as revoked.
func (s *SQLiteStore) RevokeApiKey(id string) error {
	res, err := s.db.Exec(`UPDATE api_keys SET revoked = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("api key not found: %s", id)
	}
	return nil
}

// ValidateApiKey checks a key hash against the store. If valid and not revoked,
// it updates last_used_at and returns the scopes.
func (s *SQLiteStore) ValidateApiKey(keyHash string) (*ApiKeyValidation, error) {
	var scopesStr string
	var revoked int
	err := s.db.QueryRow(
		`SELECT scopes, revoked FROM api_keys WHERE key_hash = ?`, keyHash,
	).Scan(&scopesStr, &revoked)
	if err == sql.ErrNoRows {
		return &ApiKeyValidation{Valid: false}, nil
	}
	if err != nil {
		return nil, err
	}
	if revoked != 0 {
		return &ApiKeyValidation{Valid: false}, nil
	}

	var scopes []string
	json.Unmarshal([]byte(scopesStr), &scopes)

	// Update last_used_at
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE key_hash = ?`, now, keyHash)

	return &ApiKeyValidation{Valid: true, Scopes: scopes}, nil
}
