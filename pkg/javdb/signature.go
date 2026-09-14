package javdb

import (
	"crypto/md5" //nosec - upstream app signature scheme, not a security boundary
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The JavDB mobile app API guards every endpoint with a "jdsignature" header.
// Reverse-engineered by the reference projects (JavdBviewed / JAV-JHS) it is a
// plain timestamp-tagged MD5 of "{timestamp}{salt}" with a fixed client ID:
//
//	jdsignature = "{unix}.{clientID}.{md5(unix + salt)}"
//
// Signatures are accepted for 300 seconds, so they are cached and reused.
const (
	signatureSalt = "71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e7d" +
		"5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa"
	signatureClientID = "lpw6vgqzsp"
	signatureTTL      = 300 * time.Second
)

// Signature builds a jdsignature for the given wall-clock time. Exposed for
// tests and for callers that need to mint signatures outside this package.
func Signature(at time.Time) string {
	ts := at.Unix()
	sum := md5.Sum([]byte(strconv.FormatInt(ts, 10) + signatureSalt)) //nosec
	return strconv.FormatInt(ts, 10) + "." + signatureClientID + "." + hex.EncodeToString(sum[:])
}

// signatureCache memoises the signature for signatureTTL.
type signatureCache struct {
	mu     sync.Mutex
	value  string
	expiry time.Time
	now    func() time.Time
}

func newSignatureCache() *signatureCache {
	return &signatureCache{now: time.Now}
}

// Get returns a currently-valid signature, minting a fresh one when needed.
func (c *signatureCache) Get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.value != "" && now.Before(c.expiry) {
		return c.value
	}
	c.value = Signature(now)
	// Refresh a little early so a request in flight never uses a dead signature.
	c.expiry = now.Add(signatureTTL - 15*time.Second)
	return c.value
}

// SplitSignature is a debugging helper: timestamp, client id, digest.
func SplitSignature(sig string) (ts string, clientID string, digest string) {
	parts := strings.SplitN(sig, ".", 3)
	switch len(parts) {
	case 3:
		return parts[0], parts[1], parts[2]
	case 2:
		return parts[0], parts[1], ""
	default:
		return sig, "", ""
	}
}
