package avbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/metatube-community/metatube-sdk-go/provider"
)

const maxPageBytes = 4 << 20

// HTTPError distinguishes a blocked/rate-limited upstream from an empty search.
// RetryAt is shared by all requests through this provider instance.
type HTTPError struct {
	StatusCode int
	RetryAt    time.Time
}

// RetryAfter exposes a transport-independent cooldown to the HTTP server.
func (e *HTTPError) RetryAfter() time.Time { return e.RetryAt }

func (e *HTTPError) Error() string {
	if !e.RetryAt.IsZero() {
		return fmt.Sprintf("AVBASE: HTTP %d; requests paused until %s (check AVBASE proxy/access if blocked)", e.StatusCode, e.RetryAt.Format(time.RFC3339))
	}
	return fmt.Sprintf("AVBASE: HTTP %d", e.StatusCode)
}

type pageClient struct {
	client          *http.Client
	base            string
	gate            chan struct{}
	interval        time.Duration
	timeout         time.Duration
	blockedCooldown time.Duration
	retries         int
	next            time.Time
	cooldown        *HTTPError
}

func newPageClient() *pageClient {
	jar, _ := cookiejar.New(nil)
	return &pageClient{
		client: &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone(), Jar: jar},
		base:   baseURL, gate: make(chan struct{}, 1), interval: 3 * time.Second,
		timeout: time.Minute, blockedCooldown: 5 * time.Minute, retries: 2,
	}
}

// Configuration setters are used during provider initialization, before requests.
func (ab *AVBase) SetRequestTimeout(timeout time.Duration) {
	if timeout > 0 {
		ab.pages.timeout = timeout
	}
	ab.Scraper.SetRequestTimeout(timeout)
}

func (ab *AVBase) SetProxy(rawURL string) error {
	proxyURL, err := url.Parse(rawURL)
	if err != nil || proxyURL.Host == "" {
		return errors.New("AVBASE: invalid proxy URL")
	}
	switch proxyURL.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return errors.New("AVBASE: unsupported proxy scheme")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	ab.pages.client.Transport = transport
	return nil
}

func (ab *AVBase) SetConfig(config provider.Config) error {
	for _, setting := range []struct {
		key      string
		dest     *time.Duration
		min, max time.Duration
	}{
		{"request_interval", &ab.pages.interval, time.Second, time.Minute},
		{"blocked_cooldown", &ab.pages.blockedCooldown, 30 * time.Second, 24 * time.Hour},
	} {
		if config.Has(setting.key) {
			v, err := config.GetDuration(setting.key)
			if err != nil || v < setting.min || v > setting.max {
				return fmt.Errorf("AVBASE: %s must be between %s and %s", setting.key, setting.min, setting.max)
			}
			*setting.dest = v
		}
	}
	if config.Has("max_retries") {
		v, err := config.GetInt64("max_retries")
		if err != nil || v < 0 || v > 3 {
			return errors.New("AVBASE: max_retries must be between 0 and 3")
		}
		ab.pages.retries = int(v)
	}
	if config.Has("source_enrichment") {
		v, err := config.GetBool("source_enrichment")
		if err != nil {
			return fmt.Errorf("AVBASE: source_enrichment: %w", err)
		}
		ab.sourceEnrichment = v
	}
	return nil
}

type pageData struct {
	BuildID string `json:"buildId"`
	Props   struct {
		PageProps json.RawMessage `json:"pageProps"`
	} `json:"props"`
}

func (c *pageClient) page(relative string, result any) error {
	data, err := c.document(relative)
	if err != nil {
		return err
	}
	if len(data.Props.PageProps) == 0 || string(data.Props.PageProps) == "null" {
		return errors.New("AVBASE: missing pageProps")
	}
	if err := json.Unmarshal(data.Props.PageProps, result); err != nil {
		return fmt.Errorf("AVBASE: invalid pageProps: %w", err)
	}
	return nil
}

func (c *pageClient) document(relative string) (*pageData, error) {
	body, err := c.get(relative)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("AVBASE: invalid HTML: %w", err)
	}
	raw := doc.Find("script#__NEXT_DATA__").Text()
	if raw == "" {
		return nil, errors.New("AVBASE: missing __NEXT_DATA__ (unexpected page or access challenge)")
	}
	var data pageData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("AVBASE: invalid __NEXT_DATA__: %w", err)
	}
	return &data, nil
}

func waitUntil(ctx context.Context, when time.Time) error {
	delay := time.Until(when)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryAfter(raw string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32); err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(raw); err == nil && date.After(now) {
		return date
	}
	return time.Time{}
}

// The budget includes queueing, pacing, response bodies and retries. No caller
// can turn a blocked source into an unbounded retry loop or concurrent burst.
func (c *pageClient) get(relative string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if c.cooldown != nil && time.Now().Before(c.cooldown.RetryAt) {
		return nil, c.cooldown
	}
	c.cooldown = nil
	for attempt := 0; ; attempt++ {
		if err := waitUntil(ctx, c.next); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+relative, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		req.Header.Set("Accept-Language", "ja,en;q=0.8")
		req.Header.Set("Referer", c.base)
		resp, err := c.client.Do(req)
		c.next = time.Now().Add(c.interval)
		if err != nil {
			return nil, fmt.Errorf("AVBASE: request failed: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("AVBASE: read page: %w", readErr)
		}
		now := time.Now()
		status := resp.StatusCode
		// Cloudflare may use a challenge response even with an unexpected status.
		if status == http.StatusForbidden || resp.Header.Get("Cf-Mitigated") == "challenge" {
			c.cooldown = &HTTPError{StatusCode: http.StatusForbidden, RetryAt: now.Add(c.blockedCooldown)}
			if after := retryAfter(resp.Header.Get("Retry-After"), now); after.After(c.cooldown.RetryAt) {
				c.cooldown.RetryAt = after
			}
			return nil, c.cooldown
		}
		if status == http.StatusOK {
			if len(body) > maxPageBytes {
				return nil, errors.New("AVBASE: page exceeds 4 MiB")
			}
			c.cooldown = nil
			return body, nil
		}
		failure := &HTTPError{StatusCode: status}
		retryable := status == http.StatusTooManyRequests || status == http.StatusInternalServerError || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
		if !retryable {
			return nil, failure
		}
		after := retryAfter(resp.Header.Get("Retry-After"), now)
		backoff := now.Add(time.Duration(5*(1<<attempt)) * time.Second)
		if after.Before(backoff) {
			after = backoff
		}
		if after.After(c.next) {
			c.next = after
		}
		failure.RetryAt = c.next
		c.cooldown = failure
		// Respect Retry-After across subsequent calls too, even when this call
		// has exhausted retries or cannot fit the wait in its request budget.
		deadline, _ := ctx.Deadline()
		if attempt >= c.retries || !c.next.Before(deadline) {
			return nil, failure
		}
	}
}
