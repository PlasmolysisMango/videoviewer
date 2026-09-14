package javdb

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// DefaultSites are the JavDB web domains tried, in order.
// javdb.com itself is Cloudflare-protected and unreachable in many regions;
// the numeric mirrors serve identical HTML and are usually fetchable without
// a challenge, hence they lead. Override with WithSites.
var DefaultSites = []string{
	"https://javdb.com",
	"https://javdb580.com",
	"https://javdb570.com",
}

// DefaultAPIBase is the JavDB mobile-app JSON API endpoint discovered by the
// reference browser extension. It answers with clean JSON and no Cloudflare
// interstitial, which makes it the preferred backend.
const DefaultAPIBase = "https://jdforrepam.com/api"

// Options configures a Client and its backends. Use the WithXxx helpers.
type Options struct {
	// Sites are web (HTML) backends, tried in order on every request.
	Sites []string
	// APIBase is the mobile app JSON API root, "" disables the API backend.
	APIBase string
	// UserAgent sent on every request.
	UserAgent string
	// AcceptLanguage selects the site locale: "zh-TW" (default), "zh-CN", "en".
	AcceptLanguage string
	// Cookie is a raw "k=v; k2=v2" session cookie for logged-in web scraping
	// (needed for TOP250, 热播榜, actor pages and magnet lists).
	Cookie string
	// AppToken is a "Bearer" JWT obtained from Login.
	AppToken string
	// ProxyURL routes traffic through an HTTP/SOCKS5 proxy
	// ("http://127.0.0.1:7890"). Empty means direct.
	ProxyURL string
	// Timeout bounds a single HTTP request including body download.
	Timeout time.Duration
	// MaxRetries is the number of retries after a retryable failure.
	MaxRetries int
	// Backoff is the base delay between retries (doubled each attempt).
	Backoff time.Duration
	// RateLimit is the maximum requests per second per backend; 0 disables.
	RateLimit float64
	// CacheTTL enables the in-memory result cache; <= 0 disables caching.
	CacheTTL time.Duration
	// Logger receives retry/failover diagnostics at Debug/Warn level.
	Logger *slog.Logger
	// HTTPClient overrides the built-in client (useful for tests and for
	// sharing a transport). When set, ProxyURL is ignored.
	HTTPClient *http.Client
	// Headers are extra request headers applied to every request.
	Headers map[string]string
}

// Option mutates Options.
type Option func(*Options)

// DefaultOptions returns the production defaults.
func DefaultOptions() Options {
	return Options{
		Sites:          append([]string(nil), DefaultSites...),
		APIBase:        DefaultAPIBase,
		UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		AcceptLanguage: "zh-TW,zh;q=0.9",
		Timeout:        20 * time.Second,
		MaxRetries:     2,
		Backoff:        800 * time.Millisecond,
		RateLimit:      2,
		CacheTTL:       10 * time.Minute,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// WithSites sets the web mirror list (a bare host is prefixed with https://).
func WithSites(sites ...string) Option {
	return func(o *Options) { o.Sites = normalizeSites(sites) }
}

// WithAPIBase overrides the app API root; pass "" to disable the API backend.
func WithAPIBase(base string) Option {
	return func(o *Options) { o.APIBase = strings.TrimSuffix(base, "/") }
}

// WithCookie sets the web session cookie for login-gated pages.
func WithCookie(cookie string) Option { return func(o *Options) { o.Cookie = cookie } }

// WithAppToken sets a mobile app Bearer token (see Client.Login).
func WithAppToken(token string) Option { return func(o *Options) { o.AppToken = token } }

// WithProxy routes all requests through the given proxy URL.
func WithProxy(rawURL string) Option { return func(o *Options) { o.ProxyURL = rawURL } }

// WithTimeout bounds each request.
func WithTimeout(d time.Duration) Option { return func(o *Options) { o.Timeout = d } }

// WithRetry configures retry count and base backoff.
func WithRetry(maxRetries int, backoff time.Duration) Option {
	return func(o *Options) { o.MaxRetries = maxRetries; o.Backoff = backoff }
}

// WithRateLimit caps requests per second (0 disables).
func WithRateLimit(rps float64) Option { return func(o *Options) { o.RateLimit = rps } }

// WithCache sets the result cache TTL (<=0 disables).
func WithCache(ttl time.Duration) Option { return func(o *Options) { o.CacheTTL = ttl } }

// WithUserAgent overrides the User-Agent header.
func WithUserAgent(ua string) Option { return func(o *Options) { o.UserAgent = ua } }

// WithLocale sets Accept-Language from a locale code ("zh-TW", "zh-CN", "en").
func WithLocale(locale string) Option {
	return func(o *Options) {
		switch locale {
		case "en", "en-US":
			o.AcceptLanguage = "en"
		case "zh-CN":
			o.AcceptLanguage = "zh-CN,zh;q=0.9"
		default:
			o.AcceptLanguage = "zh-TW,zh;q=0.9"
		}
	}
}

// WithLogger sets the diagnostic logger.
func WithLogger(l *slog.Logger) Option {
	return func(o *Options) {
		if l != nil {
			o.Logger = l
		}
	}
}

// WithHTTPClient injects a preconfigured client (tests, shared transports).
func WithHTTPClient(c *http.Client) Option { return func(o *Options) { o.HTTPClient = c } }

// WithHeader adds an extra header sent on every request.
func WithHeader(key, value string) Option {
	return func(o *Options) {
		if o.Headers == nil {
			o.Headers = map[string]string{}
		}
		o.Headers[key] = value
	}
}

func normalizeSites(sites []string) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "://") {
			s = "https://" + s
		}
		out = append(out, strings.TrimSuffix(s, "/"))
	}
	return out
}
