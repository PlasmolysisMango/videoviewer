package browser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewProfileLookup(t *testing.T) {
	// 默认 chrome_110 应可构造
	if _, err := New(Options{}); err != nil {
		t.Fatalf("default profile: %v", err)
	}
	// 别名写法应可解析
	if _, err := New(Options{Profile: "chrome-110"}); err != nil {
		t.Fatalf("alias chrome-110: %v", err)
	}
	if _, err := New(Options{Profile: "no_such_999"}); err == nil {
		t.Fatal("expected error for unknown profile")
	}
}

// TestAdapterAgainstLocalServer 验证 tls-client 适配器能在真实 HTTP 下完成请求，
// 且标准库 Request/Response 与 fhttp 的转换正确（不依赖外网/Cloudflare）。
func TestAdapterAgainstLocalServer(t *testing.T) {
	var gotUA, gotReferer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotReferer = r.Header.Get("Referer")
		w.Header().Set("X-Echo", r.URL.Path)
		_, _ = io.WriteString(w, "hello-from-tls-client")
	}))
	defer srv.Close()

	a, err := New(Options{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/probe", nil)
	req.Header.Set("User-Agent", "probe-ua/1.0")
	req.Header.Set("Referer", srv.URL+"/")

	resp, err := a.Do(req)
	if err != nil {
		t.Fatalf("adapter.Do: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if string(body) != "hello-from-tls-client" {
		t.Fatalf("body=%q", body)
	}
	if resp.Header.Get("X-Echo") != "/probe" {
		t.Fatalf("header conversion failed: %v", resp.Header)
	}
	if gotUA != "probe-ua/1.0" || gotReferer != srv.URL+"/" {
		t.Fatalf("request header conversion failed: ua=%q referer=%q", gotUA, gotReferer)
	}
}

// TestTransportRedirectPassthrough 验证 NewTransport 强制不跟随重定向：
// 作为 RoundTripper 使用时 3xx 原样返回，重定向交由外层 http.Client 的策略处理。
func TestTransportRedirectPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/entry":
			http.Redirect(w, r, "/home", http.StatusFound)
		case "/home":
			_, _ = io.WriteString(w, "home")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// FollowRedirect: true 应被忽略（强制 false）。
	tr, err := NewTransport(Options{Timeout: 10 * time.Second, FollowRedirect: true})
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/entry", nil)
	raw, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer raw.Body.Close()
	if raw.StatusCode != http.StatusFound || raw.Header.Get("Location") != "/home" {
		t.Fatalf("expected 302 pass-through, got status=%d location=%q", raw.StatusCode, raw.Header.Get("Location"))
	}

	redirected := 0
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		redirected++
		return nil
	}
	resp, err := client.Get(srv.URL + "/entry")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "home" || redirected != 1 {
		t.Fatalf("client-led redirect failed: status=%d body=%q redirects=%d", resp.StatusCode, body, redirected)
	}
}
