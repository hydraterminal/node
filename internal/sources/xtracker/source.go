package xtracker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hydraterminal/node/internal/config"
	"github.com/hydraterminal/node/internal/source"
)

const maxTweetsPerAccount = 20

func init() {
	source.Register("xtracker", func() source.Source { return &Source{} })
}

// Source scrapes X/Twitter using the syndication API (no auth needed for public tweets).
// For production use with cookies, a more sophisticated scraper would be needed.
type Source struct {
	cfg      config.SourceConfig
	client   *http.Client
	accounts []string
	seen     map[string]time.Time
	cookies  string
}

func (s *Source) Name() string                 { return "xtracker" }
func (s *Source) Type() source.SourceType      { return source.SourceTypePoll }
func (s *Source) Interval() time.Duration      { return s.cfg.Interval }
func (s *Source) RequiredCredentials() []string { return nil } // Cookies optional but recommended
func (s *Source) Stop() error                  { return nil }

func (s *Source) Init(cfg config.SourceConfig) error {
	s.cfg = cfg
	if s.cfg.Interval == 0 {
		s.cfg.Interval = 2 * time.Minute
	}
	s.client = &http.Client{Timeout: 15 * time.Second}
	s.accounts = cfg.Accounts
	s.seen = make(map[string]time.Time)
	s.cookies = config.GetSourceAPIKey("TWITTER_COOKIES")
	return nil
}

func (s *Source) Start(ctx context.Context, out chan<- source.CollectedBatch) error {
	start := time.Now()

	batch := source.CollectedBatch{
		Source:      "xtracker",
		CollectedAt: time.Now(),
	}

	// Clean old dedup entries
	cutoff := time.Now().Add(-2 * time.Hour)
	for k, t := range s.seen {
		if t.Before(cutoff) {
			delete(s.seen, k)
		}
	}

	for _, account := range s.accounts {
		handle := strings.TrimPrefix(account, "@")
		posts, err := s.scrapeAccount(ctx, handle)
		if err != nil {
			batch.Errors = append(batch.Errors, fmt.Sprintf("@%s: %s", handle, err))
			continue
		}
		batch.Posts = append(batch.Posts, posts...)
	}

	batch.Duration = time.Since(start)
	out <- batch
	return nil
}

func (s *Source) scrapeAccount(ctx context.Context, handle string) ([]source.NormalizedPost, error) {
	// Use the syndication timeline API (publicly accessible, no auth needed)
	timelineURL := fmt.Sprintf("https://syndication.twitter.com/srv/timeline-profile/screen-name/%s", url.PathEscape(handle))

	req, err := http.NewRequestWithContext(ctx, "GET", timelineURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	// Add cookies if available
	if s.cookies != "" {
		var cookieArr []string
		json.Unmarshal([]byte(s.cookies), &cookieArr)
		for _, c := range cookieArr {
			req.Header.Add("Cookie", c)
		}
	}

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

	return s.parseTimeline(handle, string(body))
}

var (
	tweetBlockRe = regexp.MustCompile(`data-tweet-id="(\d+)"`)
	tweetTextRe  = regexp.MustCompile(`class="timeline-Tweet-text"[^>]*>(.*?)</p>`)
	tweetTimeRe  = regexp.MustCompile(`<time[^>]*datetime="([^"]+)"`)
	htmlCleanRe  = regexp.MustCompile(`<[^>]+>`)
)

func (s *Source) parseTimeline(handle, html string) ([]source.NormalizedPost, error) {
	var posts []source.NormalizedPost

	// Split by tweet blocks
	blocks := strings.Split(html, `class="timeline-Tweet`)
	for _, block := range blocks {
		if len(posts) >= maxTweetsPerAccount {
			break
		}

		// Extract tweet ID
		idMatch := tweetBlockRe.FindStringSubmatch(block)
		if len(idMatch) < 2 {
			continue
		}
		tweetID := idMatch[1]
		externalID := fmt.Sprintf("x_%s_%s", handle, tweetID)

		// Dedup
		if _, dup := s.seen[externalID]; dup {
			continue
		}
		s.seen[externalID] = time.Now()

		// Extract text
		text := ""
		if textMatch := tweetTextRe.FindStringSubmatch(block); len(textMatch) > 1 {
			text = htmlCleanRe.ReplaceAllString(textMatch[1], " ")
			text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
		}
		if text == "" {
			continue
		}

		// Extract timestamp
		var ts time.Time
		if timeMatch := tweetTimeRe.FindStringSubmatch(block); len(timeMatch) > 1 {
			ts, _ = time.Parse(time.RFC3339, timeMatch[1])
		}

		posts = append(posts, source.NormalizedPost{
			ID:        externalID,
			Platform:  "twitter",
			Author:    handle,
			Text:      text,
			URL:       fmt.Sprintf("https://x.com/%s/status/%s", handle, tweetID),
			Timestamp: ts,
		})
	}

	return posts, nil
}
