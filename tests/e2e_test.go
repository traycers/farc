//go:build e2e

// Package tests runs `farc` and `hls_server` as two real, separately built OS
// processes talking over real TCP sockets -- something no other test in the
// repo does (internal/hlsd's TestRun_FullStack wires both sides in-process).
//
// Scope note: a live farcd process today cannot record anything through its
// real external interfaces -- internal/api has no HTTP route for
// CapturePolicy.StartRecording, a documented gap (docs/docs/archive/
// 10-capture-policy.md §8), so continuous capture never actually starts.
// This test therefore seeds a fcontainer directly into farcd's storage image
// (via internal/storage, the same way internal/hlsd's fixtures already do)
// before starting the farcd process, then exercises the real boundary
// between the two services: farcd serving TOC/candidates/frame reads over
// HTTP+WS, and hls_server turning that into a playlist and CMAF segments,
// via ADR-016's bootstrap path. It does not exercise ADR-018's live-push
// path, which would require a genuinely new write from a running farcd --
// unreachable today for the same StartRecording-gap reason.
//
// Excluded from `go build ./...` / `go test ./...` by the build tag above.
// Run explicitly with:
//
//	go test -tags e2e ./tests/... -v
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"

	"traycers/farc/fblock"
	"traycers/farc/internal/fcontainer"
	"traycers/farc/internal/hlsconfig"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
)

// Recreated from internal/hlsd/testutil_test.go (an unexported _test.go
// helper, not importable across packages) -- same convention already used
// by five other packages in this module.

func smallGeometry() storage.Geometry {
	return storage.Geometry{FblockSize: 65536, N: 4, MaxChannels: 8}
}

func smallParams() fblock.Params {
	return fblock.Params{
		FchunkSize:        1024,
		ReadChunkSize:     512,
		WriteMode:         fblock.WriteModeCyclic,
		Retention:         fblock.Retention{Days: 30},
		MinContainerShare: fblock.DefaultMinContainerShare,
	}
}

// annexB wraps each nalu with a 4-byte start code, mirroring
// internal/ingest/rtsp.go's muxAnnexB -- the format fcontainer's own video
// frame Data is always stored in.
func annexB(nalus ...[]byte) []byte {
	var out []byte
	for _, n := range nalus {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}

// A real, parseable 352x288 H.264 SPS (github.com/bluenviron/mediacommon/v2's
// own h264 test fixture) -- fmp4.Init.Marshal actually decodes the SPS (for
// width/height), unlike PPS which is only length-checked.
var testSPS = []byte{
	0x67, 0x64, 0x00, 0x0c, 0xac, 0x3b, 0x50, 0xb0,
	0x4b, 0x42, 0x00, 0x00, 0x03, 0x00, 0x02, 0x00,
	0x00, 0x03, 0x00, 0x3d, 0x08,
}
var testPPS = []byte{0x68, 0xee, 0x3c, 0x80}

type videoFrameSpec struct {
	Time uint64
	Kind uint8
	NAL  []byte
}

// writeVideoFcontainer writes one fcontainer with just a video stream for
// channel.
func writeVideoFcontainer(t *testing.T, unit *storage.Unit, channel uint32, frames []videoFrameSpec, begin, end, now uint64) {
	t.Helper()
	f := fcontainer.New()
	configID, err := f.AddStreamParams(channel, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       frames[0].Time,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   testSPS,
		ParamPPS:   testPPS,
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	fcFrames := make([]fcontainer.Frame, len(frames))
	for i, fr := range frames {
		fcFrames[i] = fcontainer.Frame{Data: annexB(fr.NAL), Time: fr.Time, Kind: fr.Kind}
	}
	err := f.AddFrames(configID, fcFrames)
	if err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	if _, err := unit.WriteFcontainer([]uint16{uint16(channel)}, begin, end, f, now); err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}
}

// prepareStorageImage builds a real, initialized Storage image on disk and
// writes one video fcontainer for channel 1 into it, then closes the image
// -- it must not be open in this test process and the farc subprocess at
// the same time.
func prepareStorageImage(t *testing.T) (imgPath string, begin, end uint64, videoFrame []byte) {
	t.Helper()
	dir := t.TempDir()
	imgPath = filepath.Join(dir, "storage.img")
	geo := smallGeometry()

	err := storage.CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	err := storage.Init(b, storage.InitConfig{Geometry: geo, Params: smallParams(), Now: 1})
	if err != nil {
		b.Close()
		t.Fatalf("Init: %v", err)
	}
	err := b.Close()
	if err != nil {
		t.Fatalf("close after init: %v", err)
	}

	b2, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	unit, err := storage.Open(storage.OpenConfig{Backend: b2})
	if err != nil {
		b2.Close()
		t.Fatalf("Open: %v", err)
	}

	begin, end = 0, 1_000_000
	videoFrame = []byte{0x65, 0xaa, 0xbb, 0xcc, 0xdd}
	writeVideoFcontainer(t, unit, 1, []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: videoFrame},
		{Time: 500_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0x11}},
	}, begin, end, 1000)

	err := unit.Close()
	if err != nil {
		t.Fatalf("close storage after writing fixture: %v", err)
	}
	return imgPath, begin, end, videoFrame
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func buildBinary(t *testing.T, pkgDir, out string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkgDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkgDir, err, output)
	}
}

// writeFarcConfig writes only the JSON-backed part of farcd's config
// (storages/channels) -- HTTP/WS/Metrics are env-sourced now
// (internal/config's package doc), so the caller passes those via
// farcEnv/startProcess instead. channelsJSON is the raw "channels" array
// literal: since hls_server now reconciles its served-channel set against
// farcd's live GET /channels (ADR-021), a channel only "exists" for
// hls_server's purposes if it's actually registered here (or created later
// via a real POST /channels) -- writing a fcontainer directly into the
// storage image (prepareStorageImage) is not enough by itself anymore.
func writeFarcConfig(t *testing.T, imgPath string, channelsJSON string) string {
	t.Helper()
	imgJSON, err := json.Marshal(imgPath)
	if err != nil {
		t.Fatalf("marshal image path: %v", err)
	}
	doc := fmt.Sprintf(`{
  "storages": [{"id":"disk0","path":%s}],
  "channels": %s
}`, imgJSON, channelsJSON)
	path := filepath.Join(t.TempDir(), "farc.config.json")
	err := os.WriteFile(path, []byte(doc), 0o644)
	if err != nil {
		t.Fatalf("write farc config: %v", err)
	}
	return path
}

// farcEnv builds the FARC_* environment variables the farc process reads
// its HTTP/WS/Metrics addresses from (internal/config's loadEnv),
// appended to os.Environ() so the subprocess still inherits PATH etc.
func farcEnv(httpPort, wsPort, metricsPort int) []string {
	return append(os.Environ(),
		"FARC_HTTP_IP=127.0.0.1",
		fmt.Sprintf("FARC_HTTP_PORT=%d", httpPort),
		"FARC_WS_IP=127.0.0.1",
		fmt.Sprintf("FARC_WS_PORT=%d", wsPort),
		"FARC_WS_MAX_CONNECTIONS=100",
		"FARC_METRICS_IP=127.0.0.1",
		fmt.Sprintf("FARC_METRICS_PORT=%d", metricsPort),
	)
}

// writeHlsConfig writes only the JSON-backed part of hls_server's config
// (channels) -- HTTP/Farcd/TargetSegmentDuration/CacheDir/CacheQuotaBytes
// are env-sourced now (internal/hlsconfig's package doc), so the caller
// passes those via hlsEnv/startProcess instead. channelsJSON is the raw
// "channels" array literal (e.g. `[{"id":1,"storage":"disk0"}]` or `[]`) --
// ADR-021 makes this only a bootstrap seed, so tests exercising live
// reconciliation can start from an empty one.
func writeHlsConfig(t *testing.T, channelsJSON string) string {
	t.Helper()
	doc := fmt.Sprintf(`{"channels": %s}`, channelsJSON)
	path := filepath.Join(t.TempDir(), "hls_server.config.json")
	err := os.WriteFile(path, []byte(doc), 0o644)
	if err != nil {
		t.Fatalf("write hls_server config: %v", err)
	}
	return path
}

// hlsEnv builds the HLS_SERVER_* environment variables the hls_server
// process reads its HTTP address, the one farcd it talks to (ADR-020), and
// segment/cache tuning from (internal/hlsconfig's loadEnv).
func hlsEnv(httpPort, farcdHTTPPort, farcdWSPort int, cacheDir string) []string {
	return append(os.Environ(),
		"HLS_SERVER_HTTP_IP=127.0.0.1",
		fmt.Sprintf("HLS_SERVER_HTTP_PORT=%d", httpPort),
		fmt.Sprintf("HLS_SERVER_FARC_HTTP=http://127.0.0.1:%d", farcdHTTPPort),
		fmt.Sprintf("HLS_SERVER_FARC_WS=ws://127.0.0.1:%d", farcdWSPort),
		"HLS_SERVER_TARGET_SEGMENT_DURATION=2s",
		"HLS_SERVER_CACHE_DIR="+cacheDir,
		"HLS_SERVER_CACHE_QUOTA_BYTES=104857600",
	)
}

// logWriter forwards a subprocess's stdout/stderr into t.Logf, prefixed by
// service name, for diagnostics when the test fails.
type logWriter struct {
	t    *testing.T
	name string
}

func (w *logWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			w.t.Logf("[%s] %s", w.name, line)
		}
	}
	return len(p), nil
}

// startProcess launches a real OS process for one of the two binaries.
// Registers a cleanup that force-kills it if the test ends without an
// explicit stopProcessGracefully call (e.g. on an earlier failure).
func startProcess(t *testing.T, name, binPath, configPath string, env []string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binPath, "-c", configPath)
	cmd.Env = env
	cmd.Stdout = &logWriter{t: t, name: name}
	cmd.Stderr = &logWriter{t: t, name: name}
	err := cmd.Start()
	if err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return cmd
}

// stopProcessGracefully sends SIGTERM (the same signal
// signal.NotifyContext(SIGINT, SIGTERM) in both cmd/*/commands/default.go
// listens for) and asserts the process exits cleanly within a bound.
func stopProcessGracefully(t *testing.T, name string, cmd *exec.Cmd) {
	t.Helper()
	err := cmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		t.Fatalf("signal %s: %v", name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s exited with error: %v", name, err)
		}
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatalf("%s did not exit gracefully after SIGTERM", name)
	}
}

func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server at %s never came up", addr)
}

func waitForPlaylist(t *testing.T, url string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastStatus int
	var lastBody []byte
	for time.Now().Before(deadline) {
		status, body := mustGet(t, url)
		if status == http.StatusOK {
			return string(body)
		}
		lastStatus, lastBody = status, body
		time.Sleep(50 * time.Millisecond)
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

// TestE2E_FarcAndHlsServerRealProcesses builds the real farc and hls_server
// binaries, seeds a storage image with one fcontainer, starts both as
// separate OS processes wired together purely through real config files and
// real TCP addresses, fetches a playlist and its segments over real HTTP,
// verifies the decoded CMAF content matches exactly what was written into
// the source fcontainer, confirms the served segments remain correct from
// cache after farcd is gone, and confirms both processes shut down cleanly
// on SIGTERM.
func TestE2E_FarcAndHlsServerRealProcesses(t *testing.T) {
	farcBin := filepath.Join(t.TempDir(), "farc")
	buildBinary(t, "../cmd/farc", farcBin)
	hlsBin := filepath.Join(t.TempDir(), "hls_server")
	buildBinary(t, "../cmd/hls_server", hlsBin)

	imgPath, begin, end, videoFrame := prepareStorageImage(t)

	farcHTTPPort := freePort(t)
	farcWSPort := freePort(t)
	farcMetricsPort := freePort(t)
	// The RTSP URL is deliberately unreachable garbage -- ChannelIngest
	// starts asynchronously and returns immediately regardless of whether it
	// ever connects (PLAN.md); this test only needs GET /channels to report
	// channel 1 so hls_server's reconciliation (ADR-021) confirms it, not
	// real capture (the fcontainer is seeded directly, prepareStorageImage).
	farcConfigPath := writeFarcConfig(t, imgPath, `[{"id":1,"rtsp_url":"rtsp://127.0.0.1:1/none","storage":"disk0","capture_policy":{"type":"continuous"}}]`)

	hlsHTTPPort := freePort(t)
	hlsConfigPath := writeHlsConfig(t, `[{"id":1,"storage":"disk0"}]`)

	farcCmd := startProcess(t, "farc", farcBin, farcConfigPath, farcEnv(farcHTTPPort, farcWSPort, farcMetricsPort))
	farcAddr := fmt.Sprintf("127.0.0.1:%d", farcHTTPPort)
	waitForServer(t, farcAddr)

	hlsCmd := startProcess(t, "hls_server", hlsBin, hlsConfigPath, hlsEnv(hlsHTTPPort, farcHTTPPort, farcWSPort, t.TempDir()))
	hlsAddr := fmt.Sprintf("127.0.0.1:%d", hlsHTTPPort)
	waitForServer(t, hlsAddr)

	playlistURL := fmt.Sprintf("http://%s/channels/1/hls/%d/%d/playlist.m3u8", hlsAddr, begin, end)
	m3u8 := waitForPlaylist(t, playlistURL)

	uris := extractSegmentURIs(m3u8)
	if len(uris) != 2 {
		t.Fatalf("playlist URIs = %v (from %s), want exactly 2 (init + one segment)", uris, m3u8)
	}

	fetchAndVerify := func() {
		var mediaData []byte
		for _, uri := range uris {
			status, data := mustGet(t, "http://"+hlsAddr+uri)
			if status != http.StatusOK {
				t.Fatalf("GET %s status = %d", uri, status)
			}
			if strings.HasSuffix(uri, "init.mp4") {
				var parsed fmp4.Init
				err := parsed.Unmarshal(bytes.NewReader(data))
				if err != nil {
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
		err := parts.Unmarshal(mediaData)
		if err != nil {
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
	}

	fetchAndVerify()

	// The whole point of internal/segmentcache: once served, a segment must
	// keep being served correctly even if farcd disappears.
	stopProcessGracefully(t, "farc", farcCmd)
	fetchAndVerify()

	stopProcessGracefully(t, "hls_server", hlsCmd)
}

// TestE2E_ChannelCreatedAndRemovedOnFarcd_ServedWithoutHlsServerRestart is
// ADR-021's real-process end-to-end proof: both binaries start with zero
// channels configured, a channel is created on the running farc process via
// its real HTTP API (POST /channels -- IngestManager.AddChannel starts the
// ingest loop asynchronously and returns immediately regardless of RTSP
// reachability, so the deliberately unreachable rtsp_url below is fine),
// and the already-running hls_server process picks it up live -- no
// restart -- because the storage image was already seeded with a
// fcontainer for channel 1 (prepareStorageImage), so reconciliation's
// bootstrap has real data to find the moment it starts tracking the
// channel. DELETE /channels/1 then confirms hls_server stops serving it,
// again without a restart.
func TestE2E_ChannelCreatedAndRemovedOnFarcd_ServedWithoutHlsServerRestart(t *testing.T) {
	farcBin := filepath.Join(t.TempDir(), "farc")
	buildBinary(t, "../cmd/farc", farcBin)
	hlsBin := filepath.Join(t.TempDir(), "hls_server")
	buildBinary(t, "../cmd/hls_server", hlsBin)

	imgPath, begin, end, _ := prepareStorageImage(t)

	farcHTTPPort := freePort(t)
	farcWSPort := freePort(t)
	farcMetricsPort := freePort(t)
	farcConfigPath := writeFarcConfig(t, imgPath, `[]`) // channel 1 is created live, via POST /channels below

	hlsHTTPPort := freePort(t)
	hlsConfigPath := writeHlsConfig(t, `[]`) // no seed -- reconciliation must discover the channel live

	farcCmd := startProcess(t, "farc", farcBin, farcConfigPath, farcEnv(farcHTTPPort, farcWSPort, farcMetricsPort))
	farcAddr := fmt.Sprintf("127.0.0.1:%d", farcHTTPPort)
	waitForServer(t, farcAddr)

	hlsCmd := startProcess(t, "hls_server", hlsBin, hlsConfigPath, hlsEnv(hlsHTTPPort, farcHTTPPort, farcWSPort, t.TempDir()))
	hlsAddr := fmt.Sprintf("127.0.0.1:%d", hlsHTTPPort)
	waitForServer(t, hlsAddr)

	playlistURL := fmt.Sprintf("http://%s/channels/1/hls/%d/%d/playlist.m3u8", hlsAddr, begin, end)

	status, body := mustGet(t, playlistURL)
	if status != http.StatusNotFound || !strings.Contains(string(body), "not configured") {
		t.Fatalf("playlist before channel creation: status=%d body=%s, want 404 \"not configured\"", status, body)
	}

	createBody, err := json.Marshal(map[string]any{
		"id": 1, "rtsp_url": "rtsp://127.0.0.1:1/none", "storage": "disk0",
		"capture_policy": map[string]any{"type": "continuous"},
	})
	if err != nil {
		t.Fatalf("marshal create-channel request: %v", err)
	}
	resp, err := http.Post("http://"+farcAddr+"/channels", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("POST /channels: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /channels status = %d, want 201 (body=%s)", resp.StatusCode, respBody)
	}

	waitForPlaylist(t, playlistURL) // hls_server picked the channel up live, no restart

	// hls_server writes its own config file back after every tracked-state
	// change (internal/hlsd's persist) -- assert the real process actually
	// did that, not just its in-memory/HTTP-visible state.
	waitForHlsConfigChannels(t, hlsConfigPath, []hlsconfig.Channel{{ID: 1, Storage: "disk0"}})

	req, err := http.NewRequest(http.MethodDelete, "http://"+farcAddr+"/channels/1", nil)
	if err != nil {
		t.Fatalf("build DELETE request: %v", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /channels/1: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /channels/1 status = %d, want 204", resp.StatusCode)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, body = mustGet(t, playlistURL)
		if status == http.StatusNotFound && strings.Contains(string(body), "not configured") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != http.StatusNotFound || !strings.Contains(string(body), "not configured") {
		t.Fatalf("playlist never became \"not configured\" again after removal: status=%d body=%s", status, body)
	}
	waitForHlsConfigChannels(t, hlsConfigPath, nil)

	stopProcessGracefully(t, "farc", farcCmd)
	stopProcessGracefully(t, "hls_server", hlsCmd)
}

// readHlsConfigChannels reads path's raw JSON "channels" list directly,
// bypassing hlsconfig.Load's env-sourced field validation (the test process
// itself doesn't set HLS_SERVER_* env -- only the hls_server subprocess
// does, via hlsEnv).
func readHlsConfigChannels(t *testing.T, path string) []hlsconfig.Channel {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wire struct {
		Channels []hlsconfig.Channel `json:"channels"`
	}
	err := json.Unmarshal(buf, &wire)
	if err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return wire.Channels
}

// waitForHlsConfigChannels polls path until its persisted channel list
// equals want or a deadline expires -- internal/hlsd's persist runs
// moments after the HTTP-visible state change, in the same reconcile
// goroutine call.
func waitForHlsConfigChannels(t *testing.T, path string, want []hlsconfig.Channel) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last []hlsconfig.Channel
	for time.Now().Before(deadline) {
		last = readHlsConfigChannels(t, path)
		if channelListsEqual(last, want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("hls_server config file channels never converged to %+v, last = %+v", want, last)
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
