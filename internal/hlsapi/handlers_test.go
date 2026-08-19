package hlsapi_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"

	"github.com/traycers/farc/internal/hlsapi"
	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/segmentcache"
	"github.com/traycers/farc/internal/tocindex"
	"github.com/traycers/farc/mediatree"
)

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

// TestServer_AddChannel_ThenRemoveChannel exercises the mutable channel set
// (ADR-021): a channel absent at construction is a clean 404 "not
// configured" (including from handlePlaylist, the one handler that used to
// skip this check), AddChannel flips it to servable without rebuilding the
// Server, and RemoveChannel flips it back.
func TestServer_AddChannel_ThenRemoveChannel(t *testing.T) {
	unit := newTestUnit(t)
	videoFrames := []videoFrameSpec{{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xaa, 0xbb}}}
	uuid := writeAVFcontainer(t, unit, 1, videoFrames, nil, 0, 1_000_000, 1000)

	farcd := newFarcdTestServer(t, unit)
	client := hlsclient.New(farcd.URL, farcd.wsURL)

	columns, err := client.GetTOC(context.Background(), "s1", uuid)
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	idx := tocindex.NewIndex()
	idx.Channel(1).Insert(tocindex.Record{UUID: uuid, StorageID: "s1", Begin: 0, End: 1_000_000, Columns: columns})

	cache, err := segmentcache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("segmentcache.New: %v", err)
	}
	const targetDur = 10 * time.Millisecond

	srv := hlsapi.New(idx, client, map[uint16]bool{}, cache, targetDur)
	hls := httptest.NewServer(srv.Handler())
	defer hls.Close()

	playlistURL := hls.URL + "/channels/1/hls/0/1000000/playlist.m3u8"

	status, body := mustGet(t, playlistURL)
	if status != http.StatusNotFound || !strings.Contains(string(body), "not configured") {
		t.Fatalf("playlist before AddChannel: status=%d body=%s, want 404 \"not configured\"", status, body)
	}

	srv.AddChannel(1)
	status, body = mustGet(t, playlistURL)
	if status != http.StatusOK {
		t.Fatalf("playlist after AddChannel: status=%d body=%s, want 200", status, body)
	}

	srv.RemoveChannel(1)
	status, body = mustGet(t, playlistURL)
	if status != http.StatusNotFound || !strings.Contains(string(body), "not configured") {
		t.Fatalf("playlist after RemoveChannel: status=%d body=%s, want 404 \"not configured\"", status, body)
	}
}

func TestServer_PlaylistThenSegments_ThenServedFromCacheAfterFarcdGone(t *testing.T) {
	unit := newTestUnit(t)
	videoFrames := []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xaa, 0xbb, 0xcc}},
		{Time: 1_000_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0xdd}},
		{Time: 2_000_000, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xee, 0xff}},
	}
	audioFrames := []audioFrameSpec{
		{Time: 0, AU: []byte{0x01, 0x02, 0x03}},
		{Time: 1_000_000, AU: []byte{0x04, 0x05}},
		{Time: 2_000_000, AU: []byte{0x06}},
	}
	uuid := writeAVFcontainer(t, unit, 1, videoFrames, audioFrames, 0, 2_100_000, 1000)

	farcd := newFarcdTestServer(t, unit)
	client := hlsclient.New(farcd.URL, farcd.wsURL)

	columns, err := client.GetTOC(context.Background(), "s1", uuid)
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	idx := tocindex.NewIndex()
	idx.Channel(1).Insert(tocindex.Record{UUID: uuid, StorageID: "s1", Begin: 0, End: 2_100_000, Columns: columns})

	cache, err := segmentcache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("segmentcache.New: %v", err)
	}
	// Larger than the whole 2.1ms fixture window, so it all fits in one
	// segment -- keeps the test's expectations simple.
	const targetDur = 10 * time.Millisecond

	srv := hlsapi.New(idx, client, map[uint16]bool{1: true}, cache, targetDur)
	hls := httptest.NewServer(srv.Handler())
	defer hls.Close()

	status, body := mustGet(t, hls.URL+"/channels/1/hls/0/2100000/playlist.m3u8")
	if status != http.StatusOK {
		t.Fatalf("playlist status = %d, want 200, body: %s", status, body)
	}
	m3u8 := string(body)
	uris := extractSegmentURIs(m3u8)
	if len(uris) != 2 {
		t.Fatalf("playlist URIs = %v (from %s), want exactly 2 (init + one segment)", uris, m3u8)
	}

	firstFetch := make(map[string][]byte, len(uris))
	for _, uri := range uris {
		status, data := mustGet(t, hls.URL+uri)
		if status != http.StatusOK {
			t.Fatalf("GET %s (cold) status = %d", uri, status)
		}
		firstFetch[uri] = data
	}

	// Validate CMAF structure: the init segment declares 2 tracks, and the
	// media segment carries 3 samples per track (all 3 fixture frames, per
	// the single-segment-covers-everything setup above).
	for _, uri := range uris {
		data := firstFetch[uri]
		if strings.HasSuffix(uri, "init.mp4") {
			var parsed fmp4.Init
			err := parsed.Unmarshal(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("fmp4.Init.Unmarshal(%s): %v", uri, err)
			}
			if len(parsed.Tracks) != 2 {
				t.Fatalf("init %s: tracks = %d, want 2", uri, len(parsed.Tracks))
			}
			continue
		}
		var parts fmp4.Parts
		err := parts.Unmarshal(data)
		if err != nil {
			t.Fatalf("fmp4.Parts.Unmarshal(%s): %v", uri, err)
		}
		if len(parts) != 1 || len(parts[0].Tracks) != 2 {
			t.Fatalf("media %s: parts = %+v, want 1 part with 2 tracks", uri, parts)
		}
		for _, tr := range parts[0].Tracks {
			if len(tr.Samples) != 3 {
				t.Fatalf("media %s: track %d samples = %d, want 3", uri, tr.ID, len(tr.Samples))
			}
		}
	}

	// Shut down the underlying farcd fixture entirely: any hlsclient call
	// from here on fails. A re-fetch of the exact same URIs must still
	// succeed, byte-for-byte, purely from segmentcache.
	farcd.Close()

	for _, uri := range uris {
		status, data := mustGet(t, hls.URL+uri)
		if status != http.StatusOK {
			t.Fatalf("GET %s (after farcd gone) status = %d, want 200 (served from cache)", uri, status)
		}
		if string(data) != string(firstFetch[uri]) {
			t.Fatalf("GET %s (after farcd gone) returned different bytes than the cold fetch", uri)
		}
	}
}
