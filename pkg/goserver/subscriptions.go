package goserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Subscription is one community list ("影單") the user pinned to the home
// screen. Stored locally (~/.videoviewer/subscriptions.json) so it survives
// restarts without writing anything to the JavDB account.
type Subscription struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MoviesCount int    `json:"movies_count,omitempty"`
}

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

// addSubscription appends a subscription, ignoring duplicates by ID.
func addSubscription(sub Subscription) error {
	if sub.ID == "" {
		return fmt.Errorf("subscription id required")
	}
	subsMu.Lock()
	defer subsMu.Unlock()
	subs := loadSubscriptions()
	for _, s := range subs {
		if s.ID == sub.ID {
			return nil // already subscribed
		}
	}
	subs = append(subs, sub)
	return saveSubscriptions(subs)
}

// removeSubscription deletes a subscription by list ID.
func removeSubscription(id string) error {
	subsMu.Lock()
	defer subsMu.Unlock()
	subs := loadSubscriptions()
	out := subs[:0]
	for _, s := range subs {
		if s.ID != id {
			out = append(out, s)
		}
	}
	return saveSubscriptions(out)
}
