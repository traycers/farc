package hlsd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"

	"traycers/farc/internal/hlsconfig"
	"traycers/farc/internal/hlsd"
	"traycers/farc/mediatree"
)

// hlsConfigPath returns a writable path for a fresh test's config file,
// bootstrapped via hlsconfig.EnsureExists exactly like
// cmd/hls_server/commands/default.go does before calling hlsd.New --
// hlsd.New never reads this path back, only writes to it later (persist), so
// its initial content doesn't need to match cfg, but the file must already
// exist as it would in production.
func hlsConfigPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hls_server.config.json")
	if err := hlsconfig.EnsureExists(path); err != nil {
		t.Fatalf("hlsconfig.EnsureExists(%s): %v", path, err)
	}
	return path
}

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

	farcd := newFarcdTestServer(t, unit, 1)

	cfg := &hlsconfig.Config{
		HTTP:  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd: hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		Channels: []hlsconfig.Channel{
			{ID: 1, Storage: "s1"},
		},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}

	h, err := hlsd.New(cfg, hlsConfigPath(t))
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

// TestRun_ChannelCreatedOnFarcd_ServedWithoutRestart is ADR-021's core
// scenario: hls_server starts with an empty seed (no channels in its own
// config), a channel is created live on farcd, and hls_server starts
// serving it without a restart.
func TestRun_ChannelCreatedOnFarcd_ServedWithoutRestart(t *testing.T) {
	unit := newTestUnit(t)
	writeVideoFcontainer(t, unit, 1, []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0x01, 0x02}},
		{Time: 500_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0x11}},
	}, 0, 1_000_000, 1000)

	farcd := newFarcdTestServer(t, unit) // no channels registered yet

	cfg := &hlsconfig.Config{
		HTTP:                  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd:                 hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}
	h, err := hlsd.New(cfg, hlsConfigPath(t))
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())

	playlistURL := "http://" + cfg.HTTP.String() + "/channels/1/hls/0/1000000/playlist.m3u8"
	waitForNotConfigured(t, playlistURL)

	addChannel(t, farcd, 1, "s1", unit)

	waitForPlaylist(t, playlistURL)
}

// TestRun_ChannelRemovedOnFarcd_StopsBeingServed is the inverse: a channel
// hls_server is already serving stops being servable once it's removed on
// farcd, without a restart.
func TestRun_ChannelRemovedOnFarcd_StopsBeingServed(t *testing.T) {
	unit := newTestUnit(t)
	writeVideoFcontainer(t, unit, 1, []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0x01, 0x02}},
		{Time: 500_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0x11}},
	}, 0, 1_000_000, 1000)

	farcd := newFarcdTestServer(t, unit, 1) // channel 1 already running

	cfg := &hlsconfig.Config{
		HTTP:                  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd:                 hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		Channels:              []hlsconfig.Channel{{ID: 1, Storage: "s1"}},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}
	h, err := hlsd.New(cfg, hlsConfigPath(t))
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())

	playlistURL := "http://" + cfg.HTTP.String() + "/channels/1/hls/0/1000000/playlist.m3u8"
	waitForPlaylist(t, playlistURL)

	removeChannel(t, farcd, 1, "s1")

	waitForNotConfigured(t, playlistURL)
}

// TestRun_ChannelMovedToDifferentStorage_ReindexesFromNewStorage is ADR-021's
// F1 regression test: without internal/tocindex.Index.Remove, a channel
// moved to a different storage would keep serving stale records pinned to
// the old storage forever, since only a full process restart used to throw
// the whole Index away. Segment URIs embed the storage id
// (/segments/{channel}/{storage}/...), so the playlist itself is enough to
// tell which storage's data is actually being served.
func TestRun_ChannelMovedToDifferentStorage_ReindexesFromNewStorage(t *testing.T) {
	unitS1 := newTestUnit(t)
	writeVideoFcontainer(t, unitS1, 1, []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xAA}},
		{Time: 500_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0x11}},
	}, 0, 1_000_000, 1000)

	unitS2 := newTestUnit(t)
	writeVideoFcontainer(t, unitS2, 1, []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xBB}},
		{Time: 500_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0x22}},
	}, 0, 1_000_000, 2000)

	farcd := newFarcdTestServer(t, unitS1, 1) // channel 1 starts on s1
	if err := farcd.reg.Register("s2", unitS2, "/dev/null"); err != nil {
		t.Fatalf("Register s2: %v", err)
	}

	cfg := &hlsconfig.Config{
		HTTP:                  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd:                 hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		Channels:              []hlsconfig.Channel{{ID: 1, Storage: "s1"}},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}
	h, err := hlsd.New(cfg, hlsConfigPath(t))
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())

	playlistURL := "http://" + cfg.HTTP.String() + "/channels/1/hls/0/1000000/playlist.m3u8"
	m3u8 := waitForPlaylist(t, playlistURL)
	for _, uri := range extractSegmentURIs(m3u8) {
		if !strings.HasPrefix(uri, "/segments/1/s1/") {
			t.Fatalf("initial segment URI = %s, want it to reference s1", uri)
		}
	}

	// Move channel 1 from s1 to s2, mirroring persistUpdatedChannel's own
	// removed(old storage) + created(new storage) event pair.
	removeChannel(t, farcd, 1, "s1")
	addChannel(t, farcd, 1, "s2", unitS2)

	deadline := time.Now().Add(3 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		m3u8 = waitForPlaylist(t, playlistURL)
		uris := extractSegmentURIs(m3u8)
		last = m3u8
		allS2 := len(uris) > 0
		for _, uri := range uris {
			if !strings.HasPrefix(uri, "/segments/1/s2/") {
				allS2 = false
			}
		}
		if allS2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("playlist never converged to serving only s2's data: %s", last)
}

// TestRun_ChannelLifecycle_PersistsToConfigFile is the config-file
// write-back scenario: hls_server rewrites configPath after every
// tracked-state change (add, remove), so the file mirrors what's actually
// being served rather than staying frozen at whatever it held at startup.
func TestRun_ChannelLifecycle_PersistsToConfigFile(t *testing.T) {
	unit := newTestUnit(t)
	writeVideoFcontainer(t, unit, 1, []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0x01, 0x02}},
		{Time: 500_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0x11}},
	}, 0, 1_000_000, 1000)

	farcd := newFarcdTestServer(t, unit) // no channels registered yet

	configPath := hlsConfigPath(t)
	cfg := &hlsconfig.Config{
		HTTP:                  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd:                 hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}
	h, err := hlsd.New(cfg, configPath)
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())

	playlistURL := "http://" + cfg.HTTP.String() + "/channels/1/hls/0/1000000/playlist.m3u8"
	waitForNotConfigured(t, playlistURL)
	waitForPersistedChannels(t, configPath, nil)

	addChannel(t, farcd, 1, "s1", unit)
	waitForPlaylist(t, playlistURL)
	waitForPersistedChannels(t, configPath, []hlsconfig.Channel{{ID: 1, Storage: "s1"}})

	removeChannel(t, farcd, 1, "s1")
	waitForNotConfigured(t, playlistURL)
	waitForPersistedChannels(t, configPath, nil)
}

// TestHlsd_ConcurrentChannelChurnAndRequests drives rapid channel add/remove
// churn concurrently with HTTP requests -- meant to be run with -race. It
// doesn't assert a specific final state (the churner never stops mid-cycle
// deterministically), only that nothing panics/deadlocks and the server
// keeps responding throughout.
func TestHlsd_ConcurrentChannelChurnAndRequests(t *testing.T) {
	unit := newTestUnit(t)
	writeVideoFcontainer(t, unit, 1, []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0x01}},
	}, 0, 1_000_000, 1000)

	farcd := newFarcdTestServer(t, unit) // channel 1 not registered yet

	cfg := &hlsconfig.Config{
		HTTP:                  hlsconfig.Addr{IP: "127.0.0.1", Port: freePort(t)},
		Farcd:                 hlsconfig.Farcd{HTTP: farcd.URL, WS: farcd.wsURL},
		TargetSegmentDuration: hlsconfig.Duration(10 * time.Millisecond),
		CacheDir:              t.TempDir(),
	}
	h, err := hlsd.New(cfg, hlsConfigPath(t))
	if err != nil {
		t.Fatalf("hlsd.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, cfg.HTTP.String())

	playlistURL := "http://" + cfg.HTTP.String() + "/channels/1/hls/0/1000000/playlist.m3u8"

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Rapidly alternate create/remove for the same channel. addChannel/
	// removeChannel call t.Fatalf on error, which is only safe from the
	// test's own goroutine -- but this loop is the only writer touching
	// channel 1, so AddChannel/RemoveChannel never race against themselves
	// and shouldn't ever actually error here.
	wg.Add(1)
	go func() {
		defer wg.Done()
		present := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			if present {
				removeChannel(t, farcd, 1, "s1")
			} else {
				addChannel(t, farcd, 1, "s1", unit)
			}
			present = !present
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := http.Get(playlistURL)
				if err != nil {
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// waitForNotConfigured polls url until it 404s with hlsapi's "not
// configured" message (as opposed to any other failure mode, e.g. "not
// indexed") -- the signal that a channel has actually stopped being served,
// not just that it has no data yet.
func waitForNotConfigured(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastStatus int
	var lastBody []byte
	for time.Now().Before(deadline) {
		status, body := mustGet(t, url)
		if status == http.StatusNotFound && strings.Contains(string(body), "not configured") {
			return
		}
		lastStatus, lastBody = status, body
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("channel never became \"not configured\": last status=%d body=%s", lastStatus, lastBody)
}

// readPersistedChannels reads path's raw JSON "channels" list directly,
// bypassing hlsconfig.Load's env-sourced field validation (which would fail
// in a test process with no HLS_SERVER_* env set).
func readPersistedChannels(t *testing.T, path string) []hlsconfig.Channel {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wire struct {
		Channels []hlsconfig.Channel `json:"channels"`
	}
	if err := json.Unmarshal(buf, &wire); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return wire.Channels
}

// waitForPersistedChannels polls path until its persisted channel list
// equals want (order-sensitive -- hlsd.persist sorts by channel id) or the
// deadline expires. Polling, rather than a single read, accounts for the
// small window between an HTTP request observing a tracked-state change
// (via hlsapi's channelSet) and hlsd.persist's own file write actually
// landing on disk, moments later in the same reconcile goroutine call.
func waitForPersistedChannels(t *testing.T, path string, want []hlsconfig.Channel) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last []hlsconfig.Channel
	for time.Now().Before(deadline) {
		last = readPersistedChannels(t, path)
		if channelListsEqual(last, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("config file channels never converged to %v, last = %v", want, last)
}

func channelListsEqual(a, b []hlsconfig.Channel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
