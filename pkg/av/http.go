package av

import (
	"context"
	"crypto/tls"
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

// defaultUserAgent 是模拟真实浏览器的 UA。可通过 Options.UserAgent 覆盖。
const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// HTTPClient 封装对目标站点的 HTTP 访问，提供：
//   - 浏览器风格请求头
//   - Cookie 会话保持
//   - 可选 HTTP/HTTPS/socks5 代理
//   - 可插拔 Requester / Transport（用于注入浏览器 TLS 指纹以绕过 Cloudflare）
//   - 预热与重试
//
// 说明：missav / jable 等站点部署在 Cloudflare 后，标准 net/http 的 TLS/HTTP2 指纹可能命中 403 挑战。
// 生产环境应通过 Options.Requester 注入带浏览器指纹的实现（见子包 videoloader/pkg/av/browser，
// 基于 bogdanfinn/tls-client 对齐 NASSAV 中 curl_cffi impersonate=chrome110 的行为）。
type HTTPClient struct {
	doer    Requester
	jar     *cookieJar
	proxy   string
	ua      string
	timeout time.Duration
	mu      sync.Mutex
	warmed  map[string]bool

	cf         map[string]CFCredential                // 归一化 host → CF 凭证（静态注入）
	cfProvider func(host string) (CFCredential, bool) // 动态提供（优先级高于 cf）
}

// CFCredential 保存某个主机通过 Cloudflare 质询后的凭证。
// cf_clearance 与“签发时的 UA + 出口 IP”绑定，故 replay 时必须携带同一 UA（本结构会锁定 UA）。
type CFCredential struct {
	// Cookie 原始 Cookie 串，如 "cf_clearance=xxx; __cf_bm=yyy"。
	Cookie string
	// UA 与该凭证绑定的 User-Agent；非空时覆盖请求 UA。
	UA string
	// Expires 过期时间；零值表示不主动判过期（依赖外部 403 重取）。
	Expires time.Time
}

// Valid 判断凭证是否仍可用（有内容且未过期）。
func (c CFCredential) Valid() bool {
	if strings.TrimSpace(c.Cookie) == "" && strings.TrimSpace(c.UA) == "" {
		return false
	}
	return c.Expires.IsZero() || time.Now().Before(c.Expires)
}

// Requester 是最小的 HTTP 执行抽象（等价于 *http.Client 的核心方法）。
// 标准库 *http.Client 与 browser 包提供的 tls-client 适配器均满足此接口，
// 因此可通过 Options.Requester 无侵入地替换底层实现。
type Requester interface {
	Do(req *http.Request) (*http.Response, error)
}

// Options 配置 HTTPClient。
type Options struct {
	// Proxy 形如 "http://127.0.0.1:7890" 或 "socks5://127.0.0.1:7891"。为空表示直连。
	// 仅在未提供 Requester 时生效。
	Proxy string
	// UserAgent 覆盖默认 UA。
	UserAgent string
	// Timeout 单次请求超时，默认 20s。
	Timeout time.Duration
	// Requester 自定义底层执行器（优先级最高）。用于注入浏览器 TLS 指纹（绕 Cloudflare）。
	// 见 videoloader/pkg/av/browser.New。
	Requester Requester
	// Transport 自定义 RoundTripper（优先级高于 Proxy）。用于注入自定义传输层。
	Transport http.RoundTripper
	// InsecureSkipVerify 跳过 TLS 校验（谨慎使用）。
	InsecureSkipVerify bool
	// CFCookies 手动/自动化注入的 Cloudflare 凭证（host → 凭证）。
	// 命中主机的请求会带上其 Cookie（含 cf_clearance）并锁定其 UA。
	CFCookies map[string]CFCredential
	// CookieProvider 动态提供 CF 凭证，优先级高于 CFCookies。
	// 适合配合 CFStore 或 webview 回填使用（见 CFStore.Provider）。
	CookieProvider func(host string) (CFCredential, bool)
}

// NewHTTPClient 构造一个 HTTPClient。
func NewHTTPClient(opt Options) (*HTTPClient, error) {
	if opt.Timeout <= 0 {
		opt.Timeout = 20 * time.Second
	}
	if opt.UserAgent == "" {
		opt.UserAgent = defaultUserAgent
	}

	c := &HTTPClient{
		jar:        newCookieJar(),
		proxy:      opt.Proxy,
		ua:         opt.UserAgent,
		timeout:    opt.Timeout,
		warmed:     map[string]bool{},
		cfProvider: opt.CookieProvider,
	}
	if len(opt.CFCookies) > 0 {
		c.cf = make(map[string]CFCredential, len(opt.CFCookies))
		for h, cred := range opt.CFCookies {
			c.cf[normalizeHost(h)] = cred
		}
	}

	// 优先使用外部注入的 Requester（例如 browser 包的 tls-client 适配器）。
	if opt.Requester != nil {
		c.doer = opt.Requester
		return c, nil
	}

	switch {
	case opt.Transport != nil:
		c.doer = &http.Client{Transport: opt.Transport, Jar: c.jar, Timeout: opt.Timeout}
	default:
		tr := &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		}
		if opt.InsecureSkipVerify {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		if opt.Proxy != "" {
			pu, err := url.Parse(opt.Proxy)
			if err != nil {
				return nil, fmt.Errorf("invalid proxy url: %w", err)
			}
			if pu.Scheme == "socks5" || pu.Scheme == "socks5h" {
				d := &net.Dialer{Timeout: opt.Timeout}
				tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
					return dialSocks5(ctx, d, pu, addr)
				}
			} else {
				tr.Proxy = http.ProxyURL(pu)
			}
		}
		c.doer = &http.Client{Transport: tr, Jar: c.jar, Timeout: opt.Timeout}
	}
	return c, nil
}

// do 执行一次请求，注入浏览器头，返回响应体字节。
func (c *HTTPClient) do(ctx context.Context, req *http.Request) ([]byte, *http.Response, error) {
	req = req.WithContext(ctx)
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.ua)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	}
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	}

	// CF 凭证注入：命中主机时带上 cf_clearance 并锁定为凭证绑定的 UA。
	// 在 doer.Do 之前完成，因此对标准库客户端与注入式 Requester（tls-client）一致生效。
	if cred, ok := c.lookupCF(req.URL.Host); ok {
		if cred.UA != "" {
			req.Header.Set("User-Agent", cred.UA)
		}
		if cred.Cookie != "" {
			if pre := req.Header.Get("Cookie"); pre != "" {
				req.Header.Set("Cookie", pre+"; "+cred.Cookie)
			} else {
				req.Header.Set("Cookie", cred.Cookie)
			}
		}
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp, err
	}
	if resp.StatusCode >= 400 {
		return body, resp, &HTTPError{Status: resp.StatusCode, URL: req.URL.String(), Snippet: snippet(string(body))}
	}
	return body, resp, nil
}

// Get 发起 GET 请求，返回响应体。referer 为空则不设置 Referer。
func (c *HTTPClient) Get(ctx context.Context, rawurl, referer string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	body, _, err := c.do(ctx, req)
	return body, err
}

// PostJSON 以 POST 发送 JSON 载荷，返回响应体。用于调用不受站点 CF 保护的后端 API（如 missav 的 Recombee）。
// headers 为附加请求头（Content-Type 默认 application/json，可被 headers 覆盖）。
func (c *HTTPClient) PostJSON(ctx context.Context, rawurl string, headers map[string]string, payload []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, rawurl, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	body, _, err := c.do(ctx, req)
	return body, err
}

// GetWithRetry 在可重试错误（429/5xx/网络错误）时按指数退避重试。
func (c *HTTPClient) GetWithRetry(ctx context.Context, rawurl, referer string, attempts int) ([]byte, error) {
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	backoff := 500 * time.Millisecond
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		body, err := c.Get(ctx, rawurl, referer)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// Warmup 通过若干次对 baseURL 的访问建立 Cookie 会话（借鉴 missav-bot 的 cookie 预热）。
func (c *HTTPClient) Warmup(ctx context.Context, baseURL string, times int) {
	if times <= 0 {
		times = 2
	}
	c.mu.Lock()
	if c.warmed[baseURL] {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	for i := 0; i < times; i++ {
		u := baseURL
		if !strings.Contains(u, "?") {
			u += "?page=2"
		}
		_, _ = c.Get(ctx, u, baseURL)
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	c.mu.Lock()
	c.warmed[baseURL] = true
	c.mu.Unlock()
}

// HTTPError 表示非 2xx 响应。
type HTTPError struct {
	Status  int
	URL     string
	Snippet string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("http %d for %s: %s", e.Status, e.URL, e.Snippet)
}

func retryable(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	return true // 网络类错误默认可重试
}

func snippet(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}
