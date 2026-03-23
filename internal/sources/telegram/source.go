package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const maxMessagesPerChannel = 20

// normalizeChannels strips any https://t.me/s/ or https://t.me/ prefix,
// accepting whatever format the user saved (full URL or bare name).
func normalizeChannels(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, ch := range raw {
		ch = strings.TrimSpace(ch)
		ch = strings.TrimPrefix(ch, "https://t.me/s/")
		ch = strings.TrimPrefix(ch, "http://t.me/s/")
		ch = strings.TrimPrefix(ch, "https://t.me/")
		ch = strings.TrimPrefix(ch, "http://t.me/")
		ch = strings.Trim(ch, "/")
		if ch != "" {
			out = append(out, ch)
		}
	}
	return out
}

func init() {
	source.Register("telegram", func() source.Source { return &Source{} })
}

type Source struct {
	cfg      config.SourceConfig
	client   *http.Client
	channels []string
	seen     map[string]time.Time
	dataDir  string
}

func (s *Source) Name() string                 { return "telegram" }
func (s *Source) Type() source.SourceType      { return source.SourceTypePoll }
func (s *Source) Interval() time.Duration      { return s.cfg.Interval }
func (s *Source) RequiredCredentials() []string { return nil }
func (s *Source) Stop() error                  { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	if s.cfg.Interval == 0 {
		s.cfg.Interval = 2 * time.Minute
	}
	s.client = &http.Client{Timeout: 15 * time.Second}
	s.channels = normalizeChannels(cfg.Channels)
	s.dataDir = cfg.DataDir
	s.seen = make(map[string]time.Time)
	s.loadSeen()
	return nil
}

func (s *Source) seenFile() string {
	if s.dataDir == "" {
		return ""
	}
	return filepath.Join(s.dataDir, "telegram_seen.json")
}

func (s *Source) loadSeen() {
	f := s.seenFile()
	if f == "" {
		return
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return // file doesn't exist yet — fine
	}
	var raw map[string]int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	cutoff := time.Now().Add(-2 * time.Hour)
	for id, ts := range raw {
		t := time.Unix(ts, 0)
		if t.After(cutoff) {
			s.seen[id] = t
		}
	}
	slog.Debug("telegram: loaded seen state", "entries", len(s.seen))
}

func (s *Source) saveSeen() {
	f := s.seenFile()
	if f == "" {
		return
	}
	raw := make(map[string]int64, len(s.seen))
	for id, t := range s.seen {
		raw[id] = t.Unix()
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(f), 0755)
	_ = os.WriteFile(f, data, 0644)
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	start := time.Now()

	batch := source.CollectedBatch{
		Source:      "telegram",
		CollectedAt: time.Now(),
	}

	// Clean old dedup entries
	cutoff := time.Now().Add(-2 * time.Hour)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}

	if len(s.channels) == 0 {
		slog.Debug("telegram: no channels configured, skipping")
		batch.Duration = time.Since(start)
		out <- batch
		return nil
	}

	for _, channel := range s.channels {
		chStart := time.Now()
		posts, err := s.scrapeChannel(ctx, channel)
		if err != nil {
			slog.Warn("telegram: channel fetch failed", "channel", channel, "error", err, "duration", time.Since(chStart).Round(time.Millisecond))
			batch.Errors = append(batch.Errors, fmt.Sprintf("%s: %s", channel, err))
			continue
		}
		newPosts := len(posts)
		slog.Debug("telegram: channel scraped", "channel", channel, "new_posts", newPosts, "duration", time.Since(chStart).Round(time.Millisecond))
		batch.Posts = append(batch.Posts, posts...)
	}

	total := len(batch.Posts)
	if total > 0 {
		slog.Info("telegram: poll done", "channels", len(s.channels), "new_posts", total, "errors", len(batch.Errors), "duration", time.Since(start).Round(time.Millisecond))
	} else {
		slog.Debug("telegram: poll done", "channels", len(s.channels), "new_posts", 0, "errors", len(batch.Errors), "duration", time.Since(start).Round(time.Millisecond))
	}

	batch.Duration = time.Since(start)
	s.saveSeen()
	out <- batch
	return nil
}

func (s *Source) scrapeChannel(ctx context.Context, channel string) ([]source.NormalizedPost, error) {
	url := fmt.Sprintf("https://t.me/s/%s", channel)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return s.parseHTML(channel, string(body))
}

// Regex patterns for HTML parsing.
// (?s) enables dotall mode so .* matches across newlines.
var (
	messageIDRe = regexp.MustCompile(`data-post="([^/]+)/(\d+)"`)
	textRe      = regexp.MustCompile(`(?s)class="tgme_widget_message_text[^"]*"[^>]*>(.*?)</div>`)
	timeRe      = regexp.MustCompile(`<time[^>]*datetime="([^"]+)"`)
	viewsRe     = regexp.MustCompile(`class="tgme_widget_message_views"[^>]*>([^<]+)`)
	photoRe     = regexp.MustCompile(`background-image:url\('([^']+)'\)`)
	htmlTagRe   = regexp.MustCompile(`<[^>]+>`)
	entityRe    = regexp.MustCompile(`&(amp|lt|gt|quot|#39);`)
)

func (s *Source) parseHTML(channel, html string) ([]source.NormalizedPost, error) {
	// Split into message blocks
	blocks := strings.Split(html, `class="tgme_widget_message_wrap`)
	if len(blocks) <= 1 {
		slog.Debug("telegram: no message blocks found in HTML", "channel", channel, "html_len", len(html))
		return nil, nil
	}
	slog.Debug("telegram: parsing blocks", "channel", channel, "blocks", len(blocks)-1)

	var posts []source.NormalizedPost
	for _, block := range blocks[1:] {
		if len(posts) >= maxMessagesPerChannel {
			break
		}

		// Extract message ID
		idMatch := messageIDRe.FindStringSubmatch(block)
		if len(idMatch) < 3 {
			continue
		}
		msgID := idMatch[2]
		externalID := fmt.Sprintf("%s_%s", channel, msgID)

		// Dedup
		if _, dup := s.seen[externalID]; dup {
			continue
		}
		s.seen[externalID] = time.Now()

		// Extract text
		text := ""
		if textMatch := textRe.FindStringSubmatch(block); len(textMatch) > 1 {
			text = stripHTML(textMatch[1])
		}
		if text == "" {
			continue
		}

		// Extract timestamp
		var ts time.Time
		if timeMatch := timeRe.FindStringSubmatch(block); len(timeMatch) > 1 {
			ts, _ = time.Parse(time.RFC3339, timeMatch[1])
		}

		// Extract views
		views := 0
		if viewMatch := viewsRe.FindStringSubmatch(block); len(viewMatch) > 1 {
			views = parseViews(strings.TrimSpace(viewMatch[1]))
		}

		// Extract photos
		var photos []string
		for _, match := range photoRe.FindAllStringSubmatch(block, -1) {
			if len(match) > 1 {
				photos = append(photos, match[1])
			}
		}

		posts = append(posts, source.NormalizedPost{
			ID:        externalID,
			Platform:  "telegram",
			Author:    channel,
			Text:      text,
			URL:       fmt.Sprintf("https://t.me/%s/%s", channel, msgID),
			Timestamp: ts,
			Views:     views,
			Photos:    photos,
		})
	}

	return posts, nil
}

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = entityRe.ReplaceAllStringFunc(s, func(m string) string {
		switch m {
		case "&amp;":
			return "&"
		case "&lt;":
			return "<"
		case "&gt;":
			return ">"
		case "&quot;":
			return `"`
		case "&#39;":
			return "'"
		}
		return m
	})
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func parseViews(s string) int {
	s = strings.TrimSpace(s)
	multiplier := 1
	if strings.HasSuffix(s, "K") {
		multiplier = 1000
		s = strings.TrimSuffix(s, "K")
	} else if strings.HasSuffix(s, "M") {
		multiplier = 1000000
		s = strings.TrimSuffix(s, "M")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(f * float64(multiplier))
}
