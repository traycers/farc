package hlsd_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"

	"traycers/farc/internal/hlsconfig"
	"traycers/farc/internal/hlsd"
	"traycers/farc/mediatree"
)

// TestRun_FullStack exercises the entire hls_server binary's wiring end to
// end (PLAN.md phase 7's verify clause): a real farcd fixture, a real hlsd
// against it (its own EventSubscriber bootstrapping the index, not a
// manually-constructed one), and actual HTTP requests for a playlist and
// its segments — with decoded frame payloads compared against exactly what
// was written into the source fcontainer.
func TestRun_FullStack(t *testing.T) {
	unit := newTestUnit(t)
	videoFrame := []byte{0x65, 0xaa, 0xbb, 0xcc, 0xdd}
	writeVideoFcontainer(t, unit, 1, []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: videoFrame},
		{Time: 500_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0x11}},
	}, 0, 1_000_000, 1000)

	farcd := newFarcdTestServer(t, unit)

	cfg := &hlsconfig.Config{
		HTTP:  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd: hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		Channels: []hlsconfig.Channel{
			{ID: 1, Storage: "s1"},
		},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}

	h, err := hlsd.New(cfg)
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- h.Run(ctx) }()

	waitForServer(t, cfg.HTTP.String())

	playlistURL := "http://" + cfg.HTTP.String() + "/channels/1/hls/0/1000000/playlist.m3u8"
	m3u8 := waitForPlaylist(t, playlistURL)

	uris := extractSegmentURIs(m3u8)
	if len(uris) != 2 {
		t.Fatalf("playlist URIs = %v (from %s), want exactly 2 (init + one segment)", uris, m3u8)
	}

	var mediaData []byte
	for _, uri := range uris {
		status, data := mustGet(t, "http://"+cfg.HTTP.String()+uri)
		if status != http.StatusOK {
			t.Fatalf("GET %s status = %d", uri, status)
		}
		if strings.HasSuffix(uri, "init.mp4") {
			var parsed fmp4.Init
			if err := parsed.Unmarshal(bytes.NewReader(data)); err != nil {
				t.Fatalf("fmp4.Init.Unmarshal(%s): %v", uri, err)
			}
			if len(parsed.Tracks) != 1 {
				t.Fatalf("init %s: tracks = %d, want 1 (video only)", uri, len(parsed.Tracks))
			}
			continue
		}
		mediaData = data
	}

	var parts fmp4.Parts
	if err := parts.Unmarshal(mediaData); err != nil {
		t.Fatalf("fmp4.Parts.Unmarshal: %v", err)
	}
	if len(parts) != 1 || len(parts[0].Tracks) != 1 || len(parts[0].Tracks[0].Samples) != 2 {
		t.Fatalf("parts = %+v, want 1 part, 1 track, 2 samples", parts)
	}
	nalus, err := parts[0].Tracks[0].Samples[0].GetH264()
	if err != nil {
		t.Fatalf("GetH264: %v", err)
	}
	if len(nalus) != 1 || !bytes.Equal(nalus[0], videoFrame) {
		t.Fatalf("decoded NAL = %x, want %x (exactly what was written to the source fcontainer)", nalus, videoFrame)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never came up", addr)
}

func waitForPlaylist(t *testing.T, url string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastStatus int
	var lastBody []byte
	for time.Now().Before(deadline) {
		status, body := mustGet(t, url)
		if status == http.StatusOK {
			return string(body)
		}
		lastStatus, lastBody = status, body
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("playlist never became available: last status=%d body=%s", lastStatus, lastBody)
	return ""
}

func mustGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", url, err)
	}
	return resp.StatusCode, body
}

// extractSegmentURIs pulls every playable URI out of a rendered .m3u8: the
// EXT-X-MAP init reference plus each EXTINF's segment line.
func extractSegmentURIs(m3u8 string) []string {
	var out []string
	for _, line := range strings.Split(m3u8, "\n") {
		if strings.HasPrefix(line, "#EXT-X-MAP:URI=\"") {
			uri := strings.TrimPrefix(line, "#EXT-X-MAP:URI=\"")
			uri = strings.TrimSuffix(uri, "\"")
			out = append(out, uri)
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}
