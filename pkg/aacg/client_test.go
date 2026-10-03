package aacg

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const healthyHomepage = `<html><head><title>51吃瓜网 - 测试首页</title></head><body><div id="index" role="main"><article itemtype="http://schema.org/BlogPosting"><a href="/article">测试条目</a></article></div><img src="/never-fetch-image"><script src="/never-fetch-script"></script></body></html>`

func testLandingHTML(t *testing.T, main, backup string) string {
	t.Helper()
	config := map[string]any{
		"domain":        []map[string]string{{"name": "主线路", "value": main, "badge": "推荐访问"}},
		"backup_domain": []map[string]string{{"name": "备用线路", "value": backup}},
		"randomDomain":  "1",
		"zz_line":       "never-generate.invalid",
	}
	plain, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("fixture-key"))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, []byte(strings.Repeat(string(byte(padding)), padding))...)
	iv := []byte("0123456789abcdef")
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, plain)
	encoded := base64.StdEncoding.EncodeToString(append(iv, encrypted...))
	return fmt.Sprintf(`<html><script>
// window.appConfig = {data: "old", key: "old"};
window.appConfig = {
  // data: "stale",
  data: %q,
  key: "fixture-key"
};
</script><script src="/never-fetch-sdk"></script></html>`, encoded)
}

func newTestClient(t *testing.T, srv *httptest.Server, entry string) *Client {
	t.Helper()
	c, err := New(Options{EntryURL: srv.URL + entry, Transport: srv.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeTestHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, body)
}

func TestDiscoverAndCheckIntegration(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var landing string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/entry":
			http.SetCookie(w, &http.Cookie{Name: "visitor", Value: "fixture", Path: "/"})
			writeTestHTML(w, `<a href="/never-follow-ad">广告</a><a id="random-id-123" href="/landing">加载中</a><script>(function(a){if(typeof(a.click)=='function'){a.click()}else{window.location.replace(a.href)}})(document.getElementById("random-id-123"))</script>`)
		case "/landing":
			if !strings.HasSuffix(r.Referer(), "/entry") {
				t.Errorf("missing entry referer: %q", r.Referer())
			}
			if cookie, err := r.Cookie("visitor"); err != nil || cookie.Value != "fixture" {
				t.Error("missing anonymous session cookie")
			}
			writeTestHTML(w, landing)
		case "/primary":
			http.Redirect(w, r, "/home", http.StatusFound)
		case "/home":
			writeTestHTML(w, healthyHomepage)
		case "/backup":
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "temporarily unavailable")
		default:
			t.Errorf("unexpected resource request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	landing = testLandingHTML(t, srv.URL+"/primary", srv.URL+"/backup")
	c := newTestClient(t, srv, "/entry")
	report, err := c.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.EntryURL != srv.URL+"/entry" || report.LandingURL != srv.URL+"/landing" || len(report.Targets) != 2 {
		t.Fatalf("unexpected discovery: %+v", report)
	}
	if report.Targets[0].Kind != "primary" || report.Targets[0].Badge != "推荐访问" || report.Targets[1].Kind != "backup" {
		t.Fatalf("lost source metadata: %+v", report.Targets)
	}
	good, err := c.Check(context.Background(), report.Targets[0])
	if err != nil {
		t.Fatal(err)
	}
	if !good.Usable || !good.Recognized || !good.HTTPAvailable || good.StatusCode != 200 || good.FinalURL != srv.URL+"/home" || good.Bytes != len(healthyHomepage) || good.ArticleCount != 1 || good.Elapsed <= 0 {
		t.Fatalf("unexpected health: %+v", good)
	}
	bad, err := c.Check(context.Background(), report.Targets[1])
	if err == nil || bad.Usable || bad.StatusCode != 503 || bad.Reason == "" || bad.Bytes == 0 {
		t.Fatalf("lost failure evidence: %+v, %v", bad, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"/entry", "/landing", "/primary", "/home", "/backup"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("requests = %v, want %v", paths, want)
	}
}

func TestCheckRejectsUnusableResponses(t *testing.T) {
	cases := []struct {
		name, contentType, body, reason string
		status                          int
	}{
		{"challenge", "text/html", `<title>Just a moment</title><div class="cf-chl-container"></div>`, "challenge", 200},
		{"branded challenge", "text/html", healthyHomepage + `<div>verify you are human</div>`, "challenge", 200},
		{"wrong site", "text/html", `<title>Other site</title><article><a href="/item">item</a></article>`, "recognized", 200},
		{"empty shell", "text/html", `<title>51吃瓜网</title><div id="app"></div>`, "recognized", 200},
		{"branded detail", "text/html", `<title>51吃瓜网</title><article itemtype="http://schema.org/BlogPosting"><a href="/">返回首页</a></article>`, "recognized", 200},
		{"non HTML", "application/json", `{}`, "expected HTML", 200},
		{"missing MIME", "", healthyHomepage, "expected HTML", 200},
		{"forbidden", "text/html", healthyHomepage, "HTTP 403", 403},
		{"server error", "text/html", healthyHomepage, "HTTP 500", 500},
		{"oversized", "text/html", strings.Repeat("x", maxBodyBytes+1), "exceeds", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header()["Content-Type"] = []string{tc.contentType}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			c := newTestClient(t, srv, "")
			health, err := c.Check(context.Background(), Target{URL: srv.URL})
			if err == nil || !strings.Contains(err.Error(), tc.reason) || health.Usable || health.StatusCode != tc.status || health.FinalURL != srv.URL {
				t.Fatalf("unexpected outcome: %+v, %v", health, err)
			}
			if health.Reason != err.Error() || health.Bytes == 0 || health.Elapsed <= 0 {
				t.Fatalf("missing failure metadata: %+v", health)
			}
		})
	}
}

func TestDiscoverRejectsWrongPages(t *testing.T) {
	for _, body := range []string{
		`<a href="https://ads.invalid/">unrelated link</a>`,
		`<script>window.appConfig = {data: "broken", key: "key"};</script>`,
		`<title>Just a moment</title>`,
	} {
		t.Run(body[:12], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeTestHTML(w, body) }))
			defer srv.Close()
			if _, err := newTestClient(t, srv, "").Discover(context.Background()); err == nil {
				t.Fatal("invalid landing page accepted")
			}
		})
	}
}

func TestDiscoverRetainsFailedLandingURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/entry" {
			http.Redirect(w, r, "/blocked", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	discovery, err := newTestClient(t, srv, "/entry").Discover(context.Background())
	if err == nil || discovery.LandingURL != srv.URL+"/blocked" {
		t.Fatalf("lost failure URL: %+v, %v", discovery, err)
	}
}

func TestForwardingURL(t *testing.T) {
	base, _ := url.Parse("https://entry.invalid/path/start")
	cases := []struct {
		name, body, want string
	}{
		{"click", `<a id="r" href="../landing"></a><script>document.getElementById('r').click()</script>`, "https://entry.invalid/landing"},
		{"replace", `<a id="r" href="//target.invalid/"></a><script>window.location.replace(document.getElementById("r").href)</script>`, "https://target.invalid/"},
		{"comments", `<a id="r" href="/wrong"></a><script>// document.getElementById('r').click()</script>`, ""},
		{"string", `<a id="r" href="/wrong"></a><script>const s="document.getElementById('r').click()";</script>`, ""},
		{"ambiguous", `<a id="r" href="/one"></a><a id="r" href="/two"></a><script>document.getElementById('r').click()</script>`, ""},
		{"no click", `<a id="r" href="/one"></a><script>document.getElementById('r')</script>`, ""},
		{"unrelated click", `<a id="r" href="/one"></a><script>document.getElementById('r'); other.click()</script>`, ""},
		{"wrapper replace", `<a id="r" href="/landing"></a><script>(function(a){window.location.replace(a.href)})(document.getElementById('r'))</script>`, "https://entry.invalid/landing"},
		{"scheme", `<a id="r" href="javascript:alert(1)"></a><script>document.getElementById('r').click()</script>`, ""},
		{"credentials", `<a id="r" href="https://user:pass@target.invalid/"></a><script>document.getElementById('r').click()</script>`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			got, err := forwardingURL(doc, base)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("unexpected forwarding URL %s", got)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestRedirectBudgetAcrossHandoffs(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/http" {
			http.Redirect(w, r, "/js", http.StatusFound)
		} else {
			writeTestHTML(w, `<a id="r" href="/http"></a><script>document.getElementById('r').click()</script>`)
		}
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv, "/http").Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirect limit") || requests.Load() != maxHops+1 {
		t.Fatalf("requests=%d, err=%v", requests.Load(), err)
	}
}

func TestCheckRedirectLimitAndUnsafeScheme(t *testing.T) {
	for _, target := range []string{"/", "file:///etc/passwd", "http://user:pass@target.invalid/"} {
		t.Run(target, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, http.StatusFound) }))
			defer srv.Close()
			health, err := newTestClient(t, srv, "").Check(context.Background(), Target{URL: srv.URL})
			if err == nil || health.Usable {
				t.Fatalf("redirect accepted: %+v, %v", health, err)
			}
		})
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	entered := make(chan struct{}, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer srv.Close()
	c, err := New(Options{EntryURL: srv.URL, Transport: srv.Client().Transport, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Check(context.Background(), Target{URL: srv.URL})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out before handler entry")
	}
	c.http.Timeout = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := c.Check(ctx, Target{URL: srv.URL})
		finished <- err
	}()
	select {
	case <-entered:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("cancel test handler never started")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not stop after cancellation")
	}
}

func TestPublicDestinations(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "::ffff:198.18.0.1", "2001:db8::1", "0.0.0.0", "224.0.0.1", "255.255.255.255", "64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b::c0a8:101"} {
		t.Run(raw, func(t *testing.T) {
			if publicIP(netip.MustParseAddr(raw)) {
				t.Fatalf("non-public IP allowed: %s", raw)
			}
			u := "http://" + net.JoinHostPort(raw, "80")
			if _, err := New(Options{EntryURL: u}); err == nil {
				t.Fatalf("non-public entry allowed: %s", u)
			}
		})
	}
	for _, raw := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "64:ff9b::808:808"} {
		if !publicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("public IP rejected: %s", raw)
		}
	}
	for _, raw := range []string{"http://localhost", "http://LOCALHOST./", "file:///tmp/test", "/relative", "https://name:pass@site.invalid", "https://site.invalid:99999", "https://site.invalid\\path"} {
		if _, err := New(Options{EntryURL: raw}); err == nil {
			t.Fatalf("invalid entry accepted: %s", raw)
		}
	}
	if _, err := dialPublic(context.Background(), "tcp", "127.0.0.1:80"); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("dialer allowed local address: %v", err)
	}
	c, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), hopKey{}, new(int))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://169.254.169.254/", nil)
	if err := c.http.CheckRedirect(req, nil); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("private redirect not blocked: %v", err)
	}
}

func TestPublicDialIPs(t *testing.T) {
	pub4 := netip.MustParseAddr("8.8.8.8")
	pub6 := netip.MustParseAddr("2606:4700:4700::1111")
	nat64Pub := netip.MustParseAddr("64:ff9b::808:808")
	priv := netip.MustParseAddr("192.168.1.1")
	nat64Priv := netip.MustParseAddr("64:ff9b::c0a8:101")
	fake := netip.MustParseAddr("198.18.0.1")
	loop := netip.MustParseAddr("127.0.0.1")
	cases := []struct {
		name string
		in   []netip.Addr
		want int
	}{
		{"mixed answer keeps public only", []netip.Addr{pub4, priv, nat64Priv, fake, pub6, nat64Pub}, 3},
		{"all non-public", []netip.Addr{priv, loop, nat64Priv}, 0},
		{"single public", []netip.Addr{pub4}, 1},
		{"fake-ip is not public", []netip.Addr{fake}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := publicDialIPs(tc.in); len(got) != tc.want {
				t.Fatalf("publicDialIPs(%v) = %v, want %d entries", tc.in, got, tc.want)
			}
		})
	}
}

func TestDialCandidates(t *testing.T) {
	pub4 := netip.MustParseAddr("8.8.8.8")
	pub6 := netip.MustParseAddr("2606:4700:4700::1111")
	fake := netip.MustParseAddr("198.18.0.1")
	fakeUnmapped := netip.MustParseAddr("198.18.0.2")
	priv := netip.MustParseAddr("192.168.1.1")
	loop := netip.MustParseAddr("127.0.0.1")
	cases := []struct {
		name string
		in   []netip.Addr
		want []netip.Addr
	}{
		{"public wins over fake-ip", []netip.Addr{fake, pub4, pub6}, []netip.Addr{pub4, pub6}},
		{"fake-ip fallback when alone", []netip.Addr{fake}, []netip.Addr{fake}},
		{"mapped fake-ip is unmapped", []netip.Addr{netip.MustParseAddr("::ffff:198.18.0.2")}, []netip.Addr{fakeUnmapped}},
		{"private only yields none", []netip.Addr{priv, loop}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dialCandidates(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("dialCandidates(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestOptionsAndURLNormalization(t *testing.T) {
	for _, proxy := range []string{"garbage", "ftp://proxy.invalid", "http://"} {
		if _, err := New(Options{Proxy: proxy}); err == nil {
			t.Fatalf("invalid proxy accepted: %q", proxy)
		}
	}
	if _, err := New(Options{Transport: http.DefaultTransport, Proxy: "http://proxy.invalid"}); err == nil {
		t.Fatal("conflicting transport/proxy accepted")
	}
	c, err := New(Options{Proxy: "http://127.0.0.1:10808"})
	if err != nil || c.entry != DefaultEntryURL {
		t.Fatalf("explicit trusted proxy rejected: %v", err)
	}
	u, err := parseURL("https://EXAMPLE.invalid/path#section")
	if err != nil || u.String() != "https://example.invalid/path" {
		t.Fatalf("unexpected normalized URL: %v, %v", u, err)
	}
}
