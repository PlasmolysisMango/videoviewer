package aacg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
)

const (
	DefaultEntryURL = "https://aacg13.com/"
	maxBodyBytes    = 2 << 20
	maxHops         = 5
)

type Options struct {
	EntryURL string
	Proxy    string
	Timeout  time.Duration
	// Transport is a trusted override; destination IP filtering belongs to the injected transport.
	Transport http.RoundTripper
}

type Target struct {
	Name  string
	URL   string
	Badge string
	Kind  string
}

type Discovery struct {
	EntryURL   string
	LandingURL string
	Targets    []Target
}

type Health struct {
	Target        Target
	FinalURL      string
	StatusCode    int
	ContentType   string
	Title         string
	Bytes         int
	Elapsed       time.Duration
	ArticleCount  int
	HTTPAvailable bool
	Recognized    bool
	Usable        bool
	Reason        string
}

type Client struct {
	http   *http.Client
	entry  string
	custom bool
}

type hopKey struct{}

func New(opt Options) (*Client, error) {
	if opt.EntryURL == "" {
		opt.EntryURL = DefaultEntryURL
	}
	entry, err := parseURL(opt.EntryURL)
	if err != nil {
		return nil, fmt.Errorf("aacg entry: %w", err)
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 20 * time.Second
	}
	if opt.Transport != nil && opt.Proxy != "" {
		return nil, fmt.Errorf("aacg: Transport and Proxy cannot both be set")
	}
	var transport http.RoundTripper = opt.Transport
	if transport == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		tr.DialContext = dialPublic
		if opt.Proxy != "" {
			proxy, err := url.Parse(opt.Proxy)
			if err != nil || proxy.Hostname() == "" {
				return nil, fmt.Errorf("aacg: invalid proxy URL")
			}
			switch proxy.Scheme {
			case "http", "https", "socks5", "socks5h":
			default:
				return nil, fmt.Errorf("aacg: unsupported proxy scheme %q", proxy.Scheme)
			}
			// An explicit proxy owns DNS resolution and must enforce its own egress policy.
			tr.Proxy = http.ProxyURL(proxy)
			tr.DialContext = (&net.Dialer{Timeout: opt.Timeout}).DialContext
		}
		transport = tr
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c := &Client{entry: entry.String(), custom: opt.Transport != nil}
	c.http = &http.Client{Transport: transport, Timeout: opt.Timeout, Jar: jar}
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := c.validateURL(req.URL.String()); err != nil {
			return err
		}
		budget := req.Context().Value(hopKey{}).(*int)
		if err := takeHop(budget); err != nil {
			return err
		}
		if len(via) > 0 && via[len(via)-1].URL.Host != req.URL.Host {
			req.Header.Del("Referer")
		}
		return nil
	}
	if err := c.validateURL(entry.String()); err != nil {
		return nil, err
	}
	return c, nil
}

func parseURL(raw string) (*url.URL, error) {
	if strings.Contains(raw, "\\") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("invalid URL characters")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || strings.Contains(u.Host, "%") {
		return nil, fmt.Errorf("expected absolute HTTP(S) URL without credentials")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid URL port")
		}
	}
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.RawFragment = ""
	return u, nil
}

func (c *Client) validateURL(raw string) error {
	u, err := parseURL(raw)
	if err != nil {
		return err
	}
	if !c.custom {
		host := strings.TrimSuffix(u.Hostname(), ".")
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return fmt.Errorf("aacg: non-public target")
		}
		if ip, err := netip.ParseAddr(host); err == nil && !publicIP(ip) {
			return fmt.Errorf("aacg: non-public target")
		}
	}
	return nil
}

var reservedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	// NAT64 well-known prefix (RFC 6052): synthesized by DNS64 on IPv6-only
	// carrier networks. Unwrap the embedded IPv4 and judge it by the same
	// public-address rules so real public destinations stay reachable while
	// embedded private/loopback addresses remain blocked.
	if ip.Is6() && netip.MustParsePrefix("64:ff9b::/96").Contains(ip) {
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, block := range reservedNetworks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

// publicDialIPs keeps only public candidates from a DNS answer. Mixed answers
// (real public records alongside synthesized NAT64 or injected non-public
// addresses) must not veto the whole dial; an empty result means no candidate
// is dialable.
func publicDialIPs(ips []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, ip := range ips {
		if publicIP(ip) {
			out = append(out, ip)
		}
	}
	return out
}

// fakeIPNet is the default Clash/sing-box TUN fake-ip range. Under such a VPN
// every lookup answers inside it and the tunnel maps the address back to the
// hostname, so these are dialable despite not being public.
var fakeIPNet = netip.MustParsePrefix("198.18.0.0/15")

func dialCandidates(ips []netip.Addr) []netip.Addr {
	if dialable := publicDialIPs(ips); len(dialable) > 0 {
		return dialable
	}
	var out []netip.Addr
	for _, ip := range ips {
		if fakeIPNet.Contains(ip.Unmap()) {
			out = append(out, ip.Unmap())
		}
	}
	return out
}

func dialPublic(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialable := dialCandidates(ips)
	if len(dialable) == 0 {
		parts := make([]string, len(ips))
		for i, ip := range ips {
			parts[i] = ip.String()
		}
		return nil, fmt.Errorf("aacg: DNS resolved to a non-public address: %s (%s)", host, strings.Join(parts, ", "))
	}
	var failures []error
	for _, ip := range dialable {
		conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		failures = append(failures, err)
	}
	return nil, fmt.Errorf("aacg: no reachable address: %w", errors.Join(failures...))
}

func takeHop(budget *int) error {
	if *budget <= 0 {
		return fmt.Errorf("aacg: redirect limit exceeded")
	}
	*budget--
	return nil
}

func (c *Client) get(ctx context.Context, raw, referer string, budget *int) ([]byte, *http.Response, error) {
	if err := c.validateURL(raw); err != nil {
		return nil, nil, err
	}
	ctx = context.WithValue(ctx, hopKey{}, budget)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, resp, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return body, resp, err
	}
	if len(body) > maxBodyBytes {
		return body, resp, fmt.Errorf("aacg: response exceeds %d bytes", maxBodyBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp, fmt.Errorf("aacg: HTTP %d", resp.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return body, resp, fmt.Errorf("aacg: expected HTML, got %q", resp.Header.Get("Content-Type"))
	}
	return body, resp, nil
}

func (c *Client) Discover(ctx context.Context) (Discovery, error) {
	result := Discovery{EntryURL: c.entry}
	current, referer, budget := c.entry, "", maxHops
	for {
		body, resp, err := c.get(ctx, current, referer, &budget)
		result.LandingURL = current
		if resp != nil && resp.Request != nil {
			result.LandingURL = resp.Request.URL.String()
		}
		if err != nil {
			return result, fmt.Errorf("aacg discover: %w", err)
		}
		current = result.LandingURL
		doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
		if err != nil {
			return result, err
		}
		targets, err := extractTargets(doc)
		if err == nil {
			for _, target := range targets {
				if err := c.validateURL(target.URL); err != nil {
					return result, fmt.Errorf("aacg target %q: %w", target.Name, err)
				}
			}
			result.Targets = targets
			return result, nil
		}
		if !errors.Is(err, errNoConfig) {
			return result, fmt.Errorf("aacg config: %w", err)
		}
		next, err := forwardingURL(doc, resp.Request.URL)
		if err != nil {
			return result, fmt.Errorf("aacg entry: %w", err)
		}
		if err := takeHop(&budget); err != nil {
			return result, err
		}
		referer, current = current, next
	}
}

func forwardingURL(doc *goquery.Document, base *url.URL) (string, error) {
	ids := make(map[string]bool)
	var tokenErr error
	doc.Find("script:not([src])").Each(func(_ int, s *goquery.Selection) {
		if tokenErr != nil {
			return
		}
		tokens, err := scriptTokens(s.Text())
		if err != nil {
			tokenErr = err
			return
		}
		for i := 0; i+5 < len(tokens); i++ {
			if !hasJSTokens(tokens, i, "document", ".", "getElementById", "(") || !tokens[i+4].quoted || !hasJSTokens(tokens, i+5, ")") {
				continue
			}
			direct := hasJSTokens(tokens, i+6, ".", "click", "(", ")") ||
				(hasJSTokens(tokens, i-6, "window", ".", "location", ".", "replace", "(") && hasJSTokens(tokens, i+6, ".", "href", ")"))
			wrapper := false
			if hasJSTokens(tokens, 0, "(", "function", "(") && !tokens[3].quoted && hasJSTokens(tokens, 4, ")", "{") && hasJSTokens(tokens, i-3, "}", ")", "(") {
				param := tokens[3].text
				for j := 6; j < i-3; j++ {
					wrapper = wrapper || hasJSTokens(tokens, j, param, ".", "click", "(", ")") ||
						hasJSTokens(tokens, j, "window", ".", "location", ".", "replace", "(", param, ".", "href", ")")
				}
			}
			if direct || wrapper {
				ids[tokens[i+4].text] = true
			}
		}
	})
	if tokenErr != nil {
		return "", tokenErr
	}
	var links []string
	doc.Find("a[id][href]").Each(func(_ int, a *goquery.Selection) {
		id, _ := a.Attr("id")
		if ids[id] {
			href, _ := a.Attr("href")
			links = append(links, href)
		}
	})
	if len(links) != 1 || strings.TrimSpace(links[0]) == "" {
		return "", fmt.Errorf("expected one script-linked forwarding anchor, found %d", len(links))
	}
	reference, err := url.Parse(links[0])
	if err != nil {
		return "", fmt.Errorf("invalid forwarding URL")
	}
	u, err := parseURL(base.ResolveReference(reference).String())
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func hasJSTokens(tokens []jsToken, start int, words ...string) bool {
	if start < 0 || start+len(words) > len(tokens) {
		return false
	}
	for i, word := range words {
		if tokens[start+i].quoted || tokens[start+i].text != word {
			return false
		}
	}
	return true
}

var challengeMarkers = []string{"just a moment", "cf-chl-", "verify you are human", "checking your browser"}

func challengePage(body []byte) bool {
	lower := strings.ToLower(string(body))
	for _, marker := range challengeMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (c *Client) Check(ctx context.Context, target Target) (health Health, err error) {
	start := time.Now()
	health.Target = target
	defer func() {
		health.Elapsed = time.Since(start)
		if err != nil {
			health.Reason = err.Error()
		}
	}()
	budget := maxHops
	body, resp, err := c.get(ctx, target.URL, "", &budget)
	health.Bytes = len(body)
	if resp != nil {
		health.StatusCode = resp.StatusCode
		health.HTTPAvailable = resp.StatusCode >= 200 && resp.StatusCode < 300
		health.ContentType = resp.Header.Get("Content-Type")
		if resp.Request != nil {
			health.FinalURL = resp.Request.URL.String()
		}
	}
	if err != nil {
		return health, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return health, err
	}
	health.Title = strings.Join(strings.Fields(doc.Find("title").First().Text()), " ")
	health.ArticleCount = doc.Find("article").Length()
	health.Recognized = strings.Contains(health.Title, "51吃瓜") && doc.Find("#index[role='main'] article[itemtype$='/BlogPosting'] a[href]").Length() > 0
	if challengePage(body) {
		return health, fmt.Errorf("aacg: challenge page")
	}
	if !health.Recognized {
		return health, fmt.Errorf("aacg: response is not a recognized target homepage")
	}
	health.Usable = true
	return health, nil
}
