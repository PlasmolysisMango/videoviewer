package javdb

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors returned (wrapped) by this package.
var (
	// ErrNotFound means the movie / actor / page does not exist.
	ErrNotFound = errors.New("javdb: not found")
	// ErrAuthRequired means the resource needs a logged-in session
	// (JavDB gates TOP250, 热播榜, actor pages and magnet lists behind login).
	ErrAuthRequired = errors.New("javdb: login required")
	// ErrChallenge means Cloudflare (or similar) served an interstitial page.
	ErrChallenge = errors.New("javdb: anti-bot challenge page")
	// ErrRateLimited means upstream answered 429 / throttled us.
	ErrRateLimited = errors.New("javdb: rate limited")
	// ErrMaintenance means the site is in maintenance mode.
	ErrMaintenance = errors.New("javdb: site under maintenance")
	// ErrUnsupported means the selected backend does not implement the operation.
	ErrUnsupported = errors.New("javdb: operation not supported by backend")
	// ErrEmptyResult means the request succeeded but returned no usable entries;
	// the aggregator treats it as "try the next backend".
	ErrEmptyResult = errors.New("javdb: empty result")
	// ErrNoSite means no reachable JavDB domain was configured.
	ErrNoSite = errors.New("javdb: no reachable site")
	// ErrInvalidQuery means caller input was not usable (empty keyword ...).
	ErrInvalidQuery = errors.New("javdb: invalid query")
)

// APIError reports a well-formed JSON envelope with success=0.
type APIError struct {
	Status  int    // HTTP status
	Code    string // API "action" field, e.g. JWTVerificationError, ResourceNotFound
	Message string // API "message" field
	Path    string // request path
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("javdb: api %s: %s (%s)", e.Path, e.Message, e.Code)
	}
	return fmt.Sprintf("javdb: api %s: %s", e.Path, e.Message)
}

// Unwrap maps well-known API codes onto sentinel errors so callers can use
// errors.Is for control flow.
func (e *APIError) Unwrap() error {
	switch e.Code {
	case "JWTVerificationError", "LoginRequired":
		return ErrAuthRequired
	case "ResourceNotFound":
		return ErrNotFound
	case "ParameterInvalid":
		return ErrInvalidQuery
	}
	if e.Status == 401 || e.Status == 403 {
		return ErrAuthRequired
	}
	if e.Status == 404 {
		return ErrNotFound
	}
	return nil
}

// SiteError records a failure for one concrete site during failover.
type SiteError struct {
	Site string
	Err  error
}

func (e *SiteError) Error() string { return fmt.Sprintf("%s: %v", e.Site, e.Err) }
func (e *SiteError) Unwrap() error { return e.Err }

// MultiError aggregates per-backend failures when every backend failed.
type MultiError struct {
	Op   string
	Errs []error
}

func (e *MultiError) Error() string {
	parts := make([]string, 0, len(e.Errs))
	for _, err := range e.Errs {
		parts = append(parts, err.Error())
	}
	return fmt.Sprintf("javdb: %s failed on all backends: %s", e.Op, strings.Join(parts, "; "))
}

// Unwrap returns the aggregate so errors.Is walks every wrapped failure.
// It reports a match when any backend error matches the target, which keeps
// errors.Is(err, ErrAuthRequired) working for aggregated failures.
func (e *MultiError) Unwrap() []error { return e.Errs }
