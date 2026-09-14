package av

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// capture 记录测试服务端最后一次收到的 UA / Cookie。
type capture struct {
	mu     sync.Mutex
	ua     string
	cookie string
}

func (c *capture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.ua = r.Header.Get("User-Agent")
		c.cookie = r.Header.Get("Cookie")
		c.mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	})
}

func (c *capture) get() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ua, c.cookie
}

func hostOf(t *testing.T, rawurl string) string {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"missav.ai:443":   "missav.ai",
		"MissAV.AI":       "missav.ai",
		".missav.ai":      "missav.ai",
		" 127.0.0.1:8080": "127.0.0.1",
		"[::1]:9090":      "[::1]",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q)=%q 期望 %q", in, got, want)
		}
	}
}

func TestCFInjectionAppliesCookieAndUALock(t *testing.T) {
	var rec capture
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	hc, err := NewHTTPClient(Options{
		CFCookies: map[string]CFCredential{
			hostOf(t, srv.URL): {Cookie: "cf_clearance=SECRET", UA: "locked-ua"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hc.Get(context.Background(), srv.URL+"/", ""); err != nil {
		t.Fatal(err)
	}
	ua, cookie := rec.get()
	if ua != "locked-ua" {
		t.Errorf("UA 未被锁定，服务端收到 %q", ua)
	}
	if cookie != "cf_clearance=SECRET" {
		t.Errorf("Cookie 未注入，服务端收到 %q", cookie)
	}
}

func TestCFScopedToHostOnly(t *testing.T) {
	var rec capture
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	// 凭证挂在另一个不相干主机上 → 不应影响本请求。
	hc, err := NewHTTPClient(Options{
		UserAgent: "default-ua",
		CFCookies: map[string]CFCredential{
			"example.com": {Cookie: "cf_clearance=X", UA: "locked-ua"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hc.Get(context.Background(), srv.URL+"/", ""); err != nil {
		t.Fatal(err)
	}
	ua, cookie := rec.get()
	if ua != "default-ua" {
		t.Errorf("非目标主机 UA 被改写: %q", ua)
	}
	if cookie != "" {
		t.Errorf("非目标主机不应带 Cookie: %q", cookie)
	}
}

func TestCFExcludedWhenExpired(t *testing.T) {
	var rec capture
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	hc, _ := NewHTTPClient(Options{
		UserAgent: "default-ua",
		CFCookies: map[string]CFCredential{
			hostOf(t, srv.URL): {Cookie: "cf_clearance=OLD", UA: "locked-ua", Expires: time.Now().Add(-time.Hour)},
		},
	})
	if _, err := hc.Get(context.Background(), srv.URL+"/", ""); err != nil {
		t.Fatal(err)
	}
	ua, cookie := rec.get()
	if ua != "default-ua" {
		t.Errorf("过期凭证仍被注入: UA=%q", ua)
	}
	if cookie != "" {
		t.Errorf("过期凭证仍带 Cookie: %q", cookie)
	}
}

func TestCFProviderTakesPriority(t *testing.T) {
	var rec capture
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	hc, _ := NewHTTPClient(Options{
		CFCookies: map[string]CFCredential{
			hostOf(t, srv.URL): {Cookie: "cf_clearance=STATIC", UA: "static-ua"},
		},
		CookieProvider: func(string) (CFCredential, bool) {
			return CFCredential{Cookie: "cf_clearance=DYNAMIC", UA: "dyn-ua"}, true
		},
	})
	if _, err := hc.Get(context.Background(), srv.URL+"/", ""); err != nil {
		t.Fatal(err)
	}
	ua, cookie := rec.get()
	if ua != "dyn-ua" || cookie != "cf_clearance=DYNAMIC" {
		t.Errorf("Provider 应优先: UA=%q Cookie=%q", ua, cookie)
	}
}

func TestCFStoreRoundTripAndProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cf_overrides.json")

	// 写入并持久化。
	s1 := NewCFStore(path)
	s1.Set("missav.ai:443", CFCredential{Cookie: "cf_clearance=STORED", UA: "store-ua"})

	// 模拟「新进程」从文件加载。
	s2 := NewCFStore(path)
	cred, ok := s2.Get("missav.ai")
	if !ok || cred.Cookie != "cf_clearance=STORED" || cred.UA != "store-ua" {
		t.Fatalf("CFStore 往返失败: %+v ok=%v", cred, ok)
	}
	if _, ok := s2.Get("other.com"); ok {
		t.Error("不应命中未存主机")
	}

	// 文件权限应为 0600（凭证敏感）。
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm() != 0o600 {
		t.Errorf("cf_overrides.json 权限=%o 期望 600", fi.Mode().Perm())
	}

	// 端到端：把 Provider 接到 HTTPClient。
	var rec capture
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	s3 := NewCFStore("")
	s3.Set(hostOf(t, srv.URL), CFCredential{Cookie: "cf_clearance=E2E", UA: "e2e-ua"})
	hc, _ := NewHTTPClient(Options{CookieProvider: s3.Provider()})
	if _, err := hc.Get(context.Background(), srv.URL+"/", ""); err != nil {
		t.Fatal(err)
	}
	ua, cookie := rec.get()
	if cookie != "cf_clearance=E2E" || ua != "e2e-ua" {
		t.Errorf("CFStore→Provider 端到端注入失败: UA=%q Cookie=%q", ua, cookie)
	}
}

func TestCFStoreDeleteAndExpiry(t *testing.T) {
	s := NewCFStore("")
	s.Set("missav.ai", CFCredential{Cookie: "cf_clearance=X", UA: "ua"})
	if _, ok := s.Get("missav.ai"); !ok {
		t.Fatal("应能读到刚写入的凭证")
	}
	s.Delete("missav.ai")
	if _, ok := s.Get("missav.ai"); ok {
		t.Error("删除后不应再读到")
	}
	// 过期视为不可读。
	s.Set("jable.tv", CFCredential{Cookie: "c", Expires: time.Now().Add(-time.Second)})
	if _, ok := s.Get("jable.tv"); ok {
		t.Error("过期凭证 Get 应返回 false")
	}
}
