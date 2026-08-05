package hlsd_test

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"traycers/farc/fblock"
	"traycers/farc/internal/api"
	"traycers/farc/internal/fcontainer"
	"traycers/farc/internal/ingest"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
)

// Recreated from internal/api/testutil_test.go (an unexported _test.go
// helper, not importable across packages) — see PLAN.md's Gap resolutions.

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

func newTestUnit(t *testing.T) *storage.Unit {
	t.Helper()
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "storage.img")
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
	u, err := storage.Open(storage.OpenConfig{Backend: b2})
	if err != nil {
		b2.Close()
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { u.Close() })
	return u
}

// annexB wraps each nalu with a 4-byte start code, mirroring
// internal/ingest/rtsp.go's muxAnnexB — the format fcontainer's own video
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
// own h264 test fixture) — fmp4.Init.Marshal actually decodes the SPS (for
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
// channel, returning its UUID.
func writeVideoFcontainer(t *testing.T, unit *storage.Unit, channel uint32, frames []videoFrameSpec, begin, end, now uint64) [16]byte {
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
	uuid, err := unit.WriteFcontainer([]uint16{uint16(channel)}, begin, end, f, now)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}
	return uuid
}

// farcdTestServer is a real HttpApiServer/EventPushServer pair (the same
// types the real farcd process wires), plus the StorageRegistry/
// IngestManager backing it, so a test can register/remove a channel after
// the fixture has already started (addChannel/removeChannel below) --
// internal/hlsd now reconciles its served-channel set against this
// fixture's real GET /channels and /events/ws (ADR-021), so a channel needs
// to actually exist here to be servable, not just in hls_server's own seed
// config.
type farcdTestServer struct {
	*httptest.Server
	wsURL string
	reg   *api.StorageRegistry
	ing   *ingest.IngestManager
	push  *api.EventPushServer
}

// newFarcdTestServer registers "s1" (backed by unit) and, for each of
// channels, an already-running channel on it (via addChannel below) before
// starting the HTTP/WS listener.
func newFarcdTestServer(t *testing.T, unit *storage.Unit, channels ...uint16) *farcdTestServer {
	t.Helper()
	reg := api.NewStorageRegistry()
	if err := reg.Register("s1", unit, "/dev/null"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ing := ingest.NewIngestManager()
	push := api.NewEventPushServer(reg)
	srv := api.NewHttpApiServer(reg, ing, push)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	fts := &farcdTestServer{Server: ts, wsURL: "ws" + strings.TrimPrefix(ts.URL, "http"), reg: reg, ing: ing, push: push}
	for _, ch := range channels {
		addChannel(t, fts, ch, "s1", unit)
	}
	return fts
}

// addChannel registers a new channel directly on farcd's IngestManager and
// publishes api.EventChannelCreated, mirroring internal/farcd.go's
// persistNewChannel (minus the HTTP layer and config persistence, which
// this fixture models neither of). AddChannel starts the ingest loop
// asynchronously and returns immediately regardless of whether its
// (deliberately unreachable) RTSP URL ever connects (PLAN.md) -- these
// tests only need GET /channels/the WS event to reflect the channel, never
// real capture.
func addChannel(t *testing.T, farcd *farcdTestServer, id uint16, storageID string, unit *storage.Unit) {
	t.Helper()
	cfg := ingest.ChannelConfig{
		Channel:      id,
		RTSPURL:      "rtsp://127.0.0.1:1/none",
		StorageID:    storageID,
		Recorder:     unit,
		PolicyType:   ingest.PolicyContinuous,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	if err := farcd.ing.AddChannel(cfg); err != nil {
		t.Fatalf("AddChannel(%d): %v", id, err)
	}
	t.Cleanup(func() { _, _ = farcd.ing.RemoveChannel(id) })
	farcd.push.PublishChannelEvent(api.ChannelEvent{Name: api.EventChannelCreated, Channel: id, Storage: storageID})
}

// removeChannel removes a channel from farcd's IngestManager and publishes
// api.EventChannelRemoved, mirroring internal/farcd.go's
// persistRemovedChannel -- for tests exercising hls_server's reaction to a
// channel disappearing.
func removeChannel(t *testing.T, farcd *farcdTestServer, id uint16, storageID string) {
	t.Helper()
	if _, err := farcd.ing.RemoveChannel(id); err != nil {
		t.Fatalf("RemoveChannel(%d): %v", id, err)
	}
	farcd.push.PublishChannelEvent(api.ChannelEvent{Name: api.EventChannelRemoved, Channel: id, Storage: storageID})
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
