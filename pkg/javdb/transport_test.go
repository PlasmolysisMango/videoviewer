package javdb

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// testTransport builds a transport pointed at the stub with fast retries.
func testTransport(t *testing.T, sites ...string) *transport {
	t.Helper()
	opts := DefaultOptions()
	opts.Sites = normalizeSites(sites)
	opts.APIBase = ""
	opts.MaxRetries = 2
	opts.Backoff = 2 * time.Millisecond
	opts.RateLimit = 0
	opts.Timeout = 5 * time.Second
	tr, err := newTransport(opts)
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	return tr
}

func TestTransportRetriesThenSucceeds(t *testing.T) {
	var calls int32
	st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeHTML(w, `<html><body>ok</body></html>`)
	})
	tr := testTransport(t, st.URL())
	body, status, err := tr.do(context.Background(), req{method: http.MethodGet, url: st.URL() + "/search"})
	requireNoErr(t, err)
	if status != http.StatusOK || !strings.Contains(string(body), "ok") {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (2 retries)", calls)
	}
}

func TestTransportGivesUpAfterRetries(t *testing.T) {
	st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	tr := testTransport(t, st.URL())
	_, _, err := tr.do(context.Background(), req{method: http.MethodGet, url: st.URL() + "/x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if st.count("/x") != 3 {
		t.Fatalf("requests = %d, want 3", st.count("/x"))
	}
}

func TestTransportClassifiesHostileResponses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
		calls  int
	}{
		{"login wall", 200, fixtureLoginWallHTML, ErrAuthRequired, 1},
		{"login wall, double quotes", 200, `<html><head><script>window.location.href="/login";</script></head></html>`, ErrAuthRequired, 1},
		{"login wall, replace()", 200, `<html><head><script>location.replace('/login?redirect_to=%2F')</script></head></html>`, ErrAuthRequired, 1},
		{"sign-in page title", 200, fixtureLoginWallTitleHTML, ErrAuthRequired, 1},
		{"simplified sign-in title", 200, `<html><head><title> 登录 | JavDB 成人影片数据库 </title></head></html>`, ErrAuthRequired, 1},
		{"registration gate", 200, `<html><head><title> 註冊 | JavDB 成人影片數據庫 </title></head></html>`, ErrAuthRequired, 1},
		{"listing page is fine", 200, `<html><head><title>検索: ssis | JavDB 成人影片數據庫</title></head><body>影片</body></html>`, nil, 1},
		{"unauthorized", 401, `nope`, ErrAuthRequired, 1},
		{"not found", 404, `not here`, ErrNotFound, 1},
		{"cf challenge", 200, `<html><head><title>Just a moment...</title></head></html>`, ErrChallenge, 3},
		{"forbidden", 403, `<html>Attention Required! | Cloudflare</html>`, ErrChallenge, 3},
		{"too many requests", 429, `slow down`, ErrRateLimited, 3},
		{"maintenance", 200, `<html>網站正在維護</html>`, ErrMaintenance, 1},
		{"locked", 423, `locked`, ErrMaintenance, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := tc.status, tc.body
			st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			})
			tr := testTransport(t, st.URL())
			_, _, err := tr.do(context.Background(), req{method: http.MethodGet, url: st.URL() + "/p"})
			if tc.want == nil {
				requireNoErr(t, err)
			} else {
				requireSentinel(t, err, tc.want)
			}
			if got := st.count("/p"); got != tc.calls {
				t.Fatalf("requests = %d, want %d", got, tc.calls)
			}
		})
	}
}

func TestTransportHonoursContextCancellation(t *testing.T) {
	st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		writeHTML(w, `<html></html>`)
	})
	tr := testTransport(t, st.URL())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := tr.do(ctx, req{method: http.MethodGet, url: st.URL() + "/x"}); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestTransportSendsSessionHeaders(t *testing.T) {
	var mu sync.Mutex
	headers := map[string]http.Header{}
	st := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers[r.URL.Path] = r.Header.Clone()
		mu.Unlock()
		writeHTML(w, `<html></html>`)
	})
	opts := DefaultOptions()
	opts.Sites = []string{st.URL()}
	opts.APIBase = st.URL() + "/api"
	opts.Cookie = "_jdb_session=abc; locale=zh-TW"
	opts.AppToken = "eyJhbGciOi.test.sig"
	opts.RateLimit = 0
	opts.Headers = map[string]string{"X-Test": "1"}
	tr, err := newTransport(opts)
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	if _, _, err := tr.do(context.Background(), req{method: http.MethodGet, url: st.URL() + "/v/abc"}); err != nil {
		t.Fatalf("do: %v", err)
	}
	if _, _, err := tr.do(context.Background(), req{method: http.MethodGet, url: opts.APIBase + "/v2/search"}); err != nil {
		t.Fatalf("api do: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	web := headers["/v/abc"]
	api := headers["/api/v2/search"]
	if web == nil || api == nil {
		t.Fatalf("recorded paths: %v", keysOf(headers))
	}
	if got := web.Get("Cookie"); got != opts.Cookie {
		t.Fatalf("cookie header: %q", got)
	}
	if !strings.HasPrefix(web.Get("User-Agent"), "Mozilla/") {
		t.Fatalf("user agent: %q", web.Get("User-Agent"))
	}
	if web.Get("Accept-Language") == "" {
		t.Fatal("accept-language missing")
	}
	if web.Get("X-Test") != "1" {
		t.Fatal("custom header missing")
	}
	// The Bearer token only goes to the API host, never to the mirrors.
	if web.Get("Authorization") != "" {
		t.Fatalf("web request leaked the app token: %q", web.Get("Authorization"))
	}
	if got := api.Get("Authorization"); got != "Bearer "+opts.AppToken {
		t.Fatalf("api authorization: %q", got)
	}
}

func TestTransportSessionStateIsSafeForConcurrentLogin(t *testing.T) {
	tr := testTransport(t, "https://example.invalid")
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			tr.setAppToken("token")
			tr.setCookie("cookie")
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_, _ = tr.session()
	}
	<-done
}

func TestRateLimiterSpacesRequests(t *testing.T) {
	lim := newRateLimiter(50) // 20ms apart
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := lim.Wait(context.Background()); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("3 requests at 50 rps took %v", elapsed)
	}
	if newRateLimiter(0) != nil {
		t.Fatal("rate limit 0 must disable the limiter")
	}
}

func TestNormalizeTransportErrorKeepsContextErrors(t *testing.T) {
	if err := normalizeTransportError(context.DeadlineExceeded); errors.Is(err, context.DeadlineExceeded) {
		return
	}
	// A plain error is passed through untouched.
	plain := errors.New("boom")
	if normalizeTransportError(plain) != plain {
		t.Fatal("unexpected rewrite")
	}
}
