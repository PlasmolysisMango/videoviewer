//go:build live

package subs

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// Run with: SUBS_PROXY=http://127.0.0.1:10808 go test -tags live -run TestLive -v
func TestLiveSearchAndDownload(t *testing.T) {
	proxy := os.Getenv("SUBS_PROXY")
	c := New(ClientOptions{Proxy: proxy, Timeout: 30 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rep, err := c.Search(ctx, "SSIS-414")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, it := range rep.Items {
		fmt.Printf("[%s] %q lang=%s size=%s dl=%d ref=%s\n", it.Source, it.Title, it.Lang, it.Size, it.Downloads, it.Ref)
	}
	for src, e := range rep.Errors {
		fmt.Printf("error from %s: %s\n", src, e)
	}

	// Prefer a Chinese variant, fall back to the first item.
	var pick *Item
	for i := range rep.Items {
		if len(rep.Items[i].Lang) >= 2 && rep.Items[i].Lang[:2] == "zh" {
			pick = &rep.Items[i]
			break
		}
	}
	if pick == nil {
		pick = &rep.Items[0]
	}
	d, err := c.Download(ctx, pick.Source, pick.Ref, "")
	if err != nil {
		t.Fatalf("download %s/%s: %v", pick.Source, pick.Ref, err)
	}
	head := string(d.Body)
	if len(head) > 120 {
		head = head[:120]
	}
	fmt.Printf("downloaded %s (lang=%s, %d bytes):\n%s\n", d.Name, d.Lang, len(d.Body), head)
}
