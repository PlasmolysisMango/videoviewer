package av

import (
	"testing"
)

const masterFixture = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=1280x720
720p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5600000,RESOLUTION=1920x1080
1080p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=854x480
480p/index.m3u8`

func TestParseMasterPlaylist(t *testing.T) {
	streams := ParseMasterPlaylist(masterFixture, "https://surrit.com/uuid/playlist.m3u8")
	if len(streams) != 3 {
		t.Fatalf("want 3 streams, got %d", len(streams))
	}
	// 应按带宽升序
	if streams[0].Bandwidth > streams[1].Bandwidth || streams[1].Bandwidth > streams[2].Bandwidth {
		t.Fatalf("not sorted asc: %+v", streams)
	}
	best, ok := highestBandwidth(streams)
	if !ok || best.QualityHeight != 1080 {
		t.Fatalf("best=%+v", best)
	}
	if best.URL != "https://surrit.com/uuid/1080p/index.m3u8" {
		t.Fatalf("bad resolved url: %q", best.URL)
	}
}

func TestParseMediaSegments(t *testing.T) {
	content := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:10
#EXTINF:10.0,
seg-0.ts
#EXTINF:10.0,
seg-1.ts
#EXT-X-ENDLIST`
	segs, enc := parseMediaSegments(content, "https://x/y/playlist.m3u8")
	if enc {
		t.Fatal("should not be encrypted")
	}
	if len(segs) != 2 {
		t.Fatalf("want 2 segs got %d", len(segs))
	}
	if segs[0].uri != "https://x/y/seg-0.ts" || segs[1].uri != "https://x/y/seg-1.ts" {
		t.Fatalf("bad uris: %+v", segs)
	}
}

func TestParseMediaSegmentsEncrypted(t *testing.T) {
	content := "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k.key\"\nseg.ts\n"
	_, enc := parseMediaSegments(content, "https://x/y/")
	if !enc {
		t.Fatal("should be encrypted")
	}
}
