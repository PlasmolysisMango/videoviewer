package javdb

import (
	"crypto/md5" //nolint:gosec // mirrors the upstream app signature scheme
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// md5Hex recomputes a digest independently of the production helper.
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

// stub is a recording httptest server: backends are pointed at it through
// WithAPIBase / WithSites, so tests assert on both the decoded result and the
// request the backend actually built.
type stub struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits map[string]int
	last map[string]recordedRequest
}

type recordedRequest struct {
	Method string
	Query  url.Values
	Form   url.Values
	Header http.Header
	Body   string
}

func newStub(t *testing.T, handler http.HandlerFunc) *stub {
	helper := &stub{hits: map[string]int{}, last: map[string]recordedRequest{}}
	helper.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		body := ""
		if raw, err := io.ReadAll(r.Body); err == nil {
			body = string(raw)
		}
		helper.mu.Lock()
		helper.hits[r.URL.Path]++
		helper.hits[anyRequest]++
		helper.last[r.URL.Path] = recordedRequest{
			Method: r.Method,
			Query:  r.URL.Query(),
			Form:   r.Form,
			Header: r.Header.Clone(),
			Body:   body,
		}
		helper.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(helper.srv.Close)
	return helper
}

const anyRequest = "*"

// URL is the stub base URL (http://127.0.0.1:port, no trailing slash).
func (s *stub) URL() string { return s.srv.URL }

// APIBase points the stub at the JSON API root used by the api backend.
func (s *stub) APIBase() string { return s.srv.URL + "/api" }

// count returns how often one path was requested.
func (s *stub) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// total returns the number of requests served so far.
func (s *stub) total() int { return s.count(anyRequest) }

// lastRequest returns the most recent request recorded for a path.
func (s *stub) lastRequest(t *testing.T, path string) recordedRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.last[path]
	if !ok {
		t.Fatalf("no request recorded for %s", path)
	}
	return rec
}

// routerHandler serves canned bodies keyed by path and 404s everything else.
// Bodies starting with `{"` are answered as JSON, the rest as HTML.
func routerHandler(routes map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPost || strings.HasPrefix(body, `{"`) {
			writeJSON(w, body)
			return
		}
		writeHTML(w, body)
	}
}

// apiBody wraps data in the {"success":1,...} envelope every app endpoint uses.
func apiBody(data string) string {
	return `{"success":1,"action":null,"message":null,"data":` + data + `}`
}

// apiErrBody builds a failure envelope ("請登錄帳號" style); the HTTP status is
// chosen by the handler, the app always answers 200 with success=0.
func apiErrBody(action, message string) string {
	return fmt.Sprintf(`{"success":0,"action":%q,"message":%q,"data":null}`, action, message)
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

func writeHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, body)
}

// requireSentinel fails the test unless err wraps want.
func requireSentinel(t *testing.T, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected %v to wrap %v", err, want)
	}
}

// requireNoErr fails on a non-nil error.
func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
