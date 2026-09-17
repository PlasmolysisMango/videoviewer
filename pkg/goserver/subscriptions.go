package goserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Subscription kinds: community list, genre tag, actor.
const (
	KindCollection = "collection"
	KindGenre      = "genre"
	KindActor      = "actor"
)

// Subscription is one item the user pinned to the home screen: a community
// list ("影單"), a genre tag or an actor. Kind defaults to collection for
// records stored before kinds existed. Stored locally
// (~/.videoviewer/subscriptions.json) so it survives restarts without
// writing anything to the JavDB account.
type Subscription struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"`
	MoviesCount int    `json:"movies_count,omitempty"`
	Avatar      string `json:"avatar,omitempty"` // actor avatar URL
	Group       string `json:"group,omitempty"`  // genre web filter group (c{N} 的 N)
}

// normalizeKind maps empty/legacy kinds to collection and rejects unknown ones.
func normalizeKind(k string) (string, error) {
	switch k {
	case "", KindCollection:
		return KindCollection, nil
	case KindGenre, KindActor:
		return k, nil
	}
	return "", fmt.Errorf("unknown subscription kind %q", k)
}

func subKey(s Subscription) string { return s.Kind + "|" + s.ID }

var subsMu sync.Mutex

func subscriptionsFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "subscriptions.json"
	}
	return filepath.Join(home, ".videoviewer", "subscriptions.json")
}

func loadSubscriptions() []Subscription {
	data, err := os.ReadFile(subscriptionsFile())
	if err != nil {
		return nil // missing file: no subscriptions yet
	}
	var subs []Subscription
	if json.Unmarshal(data, &subs) != nil {
		return nil // corrupted file: start fresh
	}
	for i := range subs { // legacy records have no kind
		if subs[i].Kind == "" {
			subs[i].Kind = KindCollection
		}
	}
	return subs
}

func saveSubscriptions(subs []Subscription) error {
	if err := os.MkdirAll(filepath.Dir(subscriptionsFile()), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return err
	}
	tmp := subscriptionsFile() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, subscriptionsFile())
}

// listSubscriptions returns the persisted subscriptions in insertion order.
func listSubscriptions() []Subscription {
	subsMu.Lock()
	defer subsMu.Unlock()
	subs := loadSubscriptions()
	if subs == nil {
		subs = []Subscription{}
	}
	return subs
}

// addSubscription appends a subscription keyed by kind+ID; re-subscribing
// refreshes the stored metadata (name/avatar/group/count) in place.
func addSubscription(sub Subscription) error {
	if sub.ID == "" {
		return fmt.Errorf("subscription id required")
	}
	kind, err := normalizeKind(sub.Kind)
	if err != nil {
		return err
	}
	sub.Kind = kind
	subsMu.Lock()
	defer subsMu.Unlock()
	subs := loadSubscriptions()
	for i, s := range subs {
		if subKey(s) == subKey(sub) {
			subs[i] = sub // already subscribed: refresh metadata
			return saveSubscriptions(subs)
		}
	}
	subs = append(subs, sub)
	return saveSubscriptions(subs)
}

// removeSubscription deletes a subscription by kind + ID.
func removeSubscription(id, kind string) error {
	k, err := normalizeKind(kind)
	if err != nil {
		return err
	}
	want := k + "|" + id
	subsMu.Lock()
	defer subsMu.Unlock()
	subs := loadSubscriptions()
	out := subs[:0]
	for _, s := range subs {
		if subKey(s) != want {
			out = append(out, s)
		}
	}
	return saveSubscriptions(out)
}
