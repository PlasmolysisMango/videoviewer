package javdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// transport is the shared HTTP layer used by every backend: browser-like
// headers, retry with backoff, an optional rate limiter and page-level
// error classification (login wall / CF challenge / maintenance).
type transport struct {
	opts Options
	hc   *http.Client
	lim  *rateLimiter
	sig  *signatureCache

	// mu guards the mutable session state (app JWT / web cookie) that Login
	// updates at runtime.
	mu       sync.RWMutex
	cookie   string
	appToken string
}

type req struct {
	method      string
	url         string
	query       url.Values
	header      http.Header
	body        []byte
	contentType string
	noAuth      bool // skip session cookie / app token (login endpoint)
}

func newTransport(opts Options) (*transport, error) {
	t := &transport{opts: opts, sig: newSignatureCache()}
	if opts.HTTPClient != nil {
		t.hc = opts.HTTPClient
	} else {
		rt := http.DefaultTransport.(*http.Transport).Clone()
		if opts.ProxyURL != "" {
			pu, err := url.Parse(opts.ProxyURL)
			if err != nil {
				return nil, fmt.Errorf("javdb: invalid proxy url: %w", err)
			}
			rt.Proxy = http.ProxyURL(pu)
		}
		t.hc = &http.Client{
			Transport: rt,
			Timeout:   opts.Timeout,
			// JavDB answers login walls with a 302 to /login; the caller must
			// see that rather than silently parse the sign-in form.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return http.ErrUseLastResponse
				}
				return nil
			},
		}
	}
	if opts.RateLimit > 0 {
		t.lim = newRateLimiter(opts.RateLimit)
	}
	t.cookie = opts.Cookie
	t.appToken = opts.AppToken
	return t, nil
}

// setAppToken stores a freshly minted app JWT for subsequent requests.
func (t *transport) setAppToken(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.appToken = token
}

// setCookie stores a web session cookie obtained by a login flow.
func (t *transport) setCookie(cookie string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cookie != "" {
		t.cookie = cookie
	}
}

// session returns the current auth material.
func (t *transport) session() (cookie, appToken string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cookie, t.appToken
}

// clearSession forgets all auth material (logout).
func (t *transport) clearSession() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cookie, t.appToken = "", ""
}

// Site is a configured web mirror, defaulting to the first entry.
func (t *transport) Site() string {
	if len(t.opts.Sites) > 0 {
		return t.opts.Sites[0]
	}
	return ""
}

// do performs one request with retries and returns the validated body.
func (t *transport) do(ctx context.Context, r req) ([]byte, int, error) {
	if t.lim != nil {
		if err := t.lim.Wait(ctx); err != nil {
			return nil, 0, err
		}
	}
	var lastErr error
	for attempt := 0; attempt <= t.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(t.backoff(attempt)):
			}
		}
		body, status, err := t.attempt(ctx, r)
		switch {
		case err == nil:
			return body, status, nil
		case errors.Is(err, ErrAuthRequired), errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalidQuery), errors.Is(err, ErrMaintenance):
			// Deterministic answers: never retry, never failover-blindly.
			return nil, status, err
		default:
			lastErr = err
			t.opts.Logger.Warn("javdb request failed", "url", r.url, "attempt", attempt+1, "err", err)
			if ctx.Err() != nil {
				return nil, status, ctx.Err()
			}
		}
	}
	return nil, 0, lastErr
}

func (t *transport) backoff(attempt int) time.Duration {
	d := t.opts.Backoff
	if d <= 0 {
		d = 500 * time.Millisecond
	}
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > 15*time.Second {
			return d
		}
	}
	return d
}

func (t *transport) attempt(ctx context.Context, r req) ([]byte, int, error) {
	full := r.url
	if len(r.query) > 0 {
		sep := "?"
		if strings.Contains(full, "?") {
			sep = "&"
		}
		full += sep + r.query.Encode()
	}
	var bodyBytes io.Reader
	if r.body != nil {
		bodyBytes = bytes.NewReader(r.body)
	}
	reqCtx := ctx
	if t.opts.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, t.opts.Timeout)
		defer cancel()
	}
	httpReq, err := http.NewRequestWithContext(reqCtx, r.method, full, bodyBytes)
	if err != nil {
		return nil, 0, err
	}
	t.decorate(httpReq, r)

	resp, err := t.hc.Do(httpReq)
	if err != nil {
		return nil, 0, normalizeTransportError(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if err := classify(resp, data); err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 500 {
		return nil, resp.StatusCode, fmt.Errorf("javdb: %s: upstream status %d", resp.Request.URL.Path, resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}

func (t *transport) decorate(h *http.Request, r req) {
	h.Header.Set("User-Agent", t.opts.UserAgent)
	h.Header.Set("Accept-Language", t.opts.AcceptLanguage)
	h.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	h.Header.Set("Cache-Control", "no-cache")
	if r.contentType != "" {
		h.Header.Set("Content-Type", r.contentType)
	}
	for k, v := range t.opts.Headers {
		h.Header.Set(k, v)
	}
	for k, vals := range r.header {
		for _, v := range vals {
			h.Header.Set(k, v)
		}
	}
	if r.noAuth {
		return
	}
	cookie, appToken := t.session()
	if cookie != "" {
		h.Header.Set("Cookie", cookie)
	}
	if appToken != "" && t.opts.APIBase != "" && strings.Contains(r.url, t.opts.APIBase) {
		h.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(appToken, "Bearer "))
	}
}

// normalizeTransportError converts network/timeout failures into comparable,
// retryable errors (retryable = anything not mapped to a sentinel above).
func normalizeTransportError(err error) error {
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return fmt.Errorf("javdb: request timeout: %w", err)
	}
	return err
}

// classify maps hostile responses (login wall, CF interstitial, maintenance,
// rails 404 page) onto sentinel errors.
func classify(resp *http.Response, body []byte) error {
	head := body
	if len(head) > 4096 {
		head = head[:4096]
	}
	sniff := string(head)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case resp.StatusCode == http.StatusForbidden:
		return ErrChallenge
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrAuthRequired
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusLocked || strings.Contains(sniff, "Manage Access"):
		return ErrMaintenance
	}
	if isLoginWall(sniff) {
		return ErrAuthRequired
	}
	if isChallengePage(sniff) {
		return ErrChallenge
	}
	if isMaintenancePage(sniff) {
		return ErrMaintenance
	}
	return nil
}

// isLoginWall detects both the JS redirect stub and the sign-in page. The live
// site pads the title with spaces ("<title> 登入 | JavDB 成人影片數據庫 </title>"),
// so the check runs on the extracted title instead of a raw byte match.
func isLoginWall(sniff string) bool {
	if strings.Contains(sniff, "window.location.href='/login'") ||
		strings.Contains(sniff, `window.location.href="/login"`) ||
		strings.Contains(sniff, "location.replace('/login") {
		return true
	}
	title := pageTitle(sniff)
	if title == "" {
		return false
	}
	for _, marker := range []string{"登入 | JavDB", "登录 | JavDB", "Login | JavDB", "註冊 | JavDB", "注册 | JavDB"} {
		if strings.Contains(title, marker) {
			return true
		}
	}
	return false
}

// pageTitle returns the trimmed contents of the first <title> element.
func pageTitle(sniff string) string {
	i := strings.Index(sniff, "<title")
	if i < 0 {
		return ""
	}
	rest := sniff[i:]
	j := strings.Index(rest, ">")
	if j < 0 {
		return ""
	}
	rest = rest[j+1:]
	if k := strings.Index(rest, "</title>"); k >= 0 {
		return strings.TrimSpace(rest[:k])
	}
	return ""
}

// isChallengePage detects Cloudflare interstitials and the site's own
// fingerprint ("sensor") gate.
func isChallengePage(sniff string) bool {
	return strings.Contains(sniff, "Just a moment") ||
		strings.Contains(sniff, "cf-challenge") ||
		strings.Contains(sniff, "turnstile") ||
		strings.Contains(sniff, "Checking your browser") ||
		strings.Contains(sniff, "Attention Required! | Cloudflare")
}

func isMaintenancePage(sniff string) bool {
	return strings.Contains(sniff, "網站正在維護") ||
		strings.Contains(sniff, "网站正在维护") ||
		strings.Contains(sniff, "<title>Maintenance")
}

// rateLimiter is a tiny min-interval limiter (no external dependency).
type rateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func newRateLimiter(rps float64) *rateLimiter {
	if rps <= 0 {
		return nil
	}
	return &rateLimiter{interval: time.Duration(float64(time.Second) / rps)}
}

func (l *rateLimiter) Wait(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	wait := time.Until(l.last.Add(l.interval))
	if wait > 0 {
		l.last = l.last.Add(l.interval)
	} else {
		l.last = time.Now()
	}
	l.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
