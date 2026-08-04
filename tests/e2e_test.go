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
	if err := f.AddFrames(configID, fcFrames); err != nil {
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

	if err := storage.CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644); err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	if err := storage.Init(b, storage.InitConfig{Geometry: geo, Params: smallParams(), Now: 1}); err != nil {
		b.Close()
		t.Fatalf("Init: %v", err)
	}
	if err := b.Close(); err != nil {
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

	if err := unit.Close(); err != nil {
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

func writeFarcConfig(t *testing.T, imgPath string, httpPort, wsPort, metricsPort int) string {
	t.Helper()
	imgJSON, err := json.Marshal(imgPath)
	if err != nil {
		t.Fatalf("marshal image path: %v", err)
	}
	doc := fmt.Sprintf(`{
  "http": {"ip":"127.0.0.1","port":%d},
  "ws": {"ip":"127.0.0.1","port":%d,"max_connections":100},
  "metrics": {"ip":"127.0.0.1","port":%d},
  "storages": [{"id":"disk0","path":%s}],
  "channels": []
}`, httpPort, wsPort, metricsPort, imgJSON)
	path := filepath.Join(t.TempDir(), "farc.config.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write farc config: %v", err)
	}
	return path
}

func writeHlsConfig(t *testing.T, httpPort, farcdHTTPPort, farcdWSPort int, cacheDir string) string {
	t.Helper()
	cacheDirJSON, err := json.Marshal(cacheDir)
	if err != nil {
		t.Fatalf("marshal cache dir: %v", err)
	}
	doc := fmt.Sprintf(`{
  "http": {"ip":"127.0.0.1","port":%d},
  "farcds": [{"id":"farcd0","http":"http://127.0.0.1:%d","ws":"ws://127.0.0.1:%d"}],
  "channels": [{"id":1,"farcd":"farcd0","storage":"disk0"}],
  "target_segment_duration":"2s",
  "cache_dir":%s,
  "cache_quota_bytes":104857600
}`, httpPort, farcdHTTPPort, farcdWSPort, cacheDirJSON)
	path := filepath.Join(t.TempDir(), "hls_server.config.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write hls_server config: %v", err)
	}
	return path
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
func startProcess(t *testing.T, name, binPath, configPath string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binPath, "-c", configPath)
	cmd.Stdout = &logWriter{t: t, name: name}
	cmd.Stderr = &logWriter{t: t, name: name}
	if err := cmd.Start(); err != nil {
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
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
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
	farcConfigPath := writeFarcConfig(t, imgPath, farcHTTPPort, farcWSPort, farcMetricsPort)

	hlsHTTPPort := freePort(t)
	hlsConfigPath := writeHlsConfig(t, hlsHTTPPort, farcHTTPPort, farcWSPort, t.TempDir())

	farcCmd := startProcess(t, "farc", farcBin, farcConfigPath)
	farcAddr := fmt.Sprintf("127.0.0.1:%d", farcHTTPPort)
	waitForServer(t, farcAddr)

	hlsCmd := startProcess(t, "hls_server", hlsBin, hlsConfigPath)
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
	}

	fetchAndVerify()

	// The whole point of internal/segmentcache: once served, a segment must
	// keep being served correctly even if farcd disappears.
	stopProcessGracefully(t, "farc", farcCmd)
	fetchAndVerify()

	stopProcessGracefully(t, "hls_server", hlsCmd)
}
