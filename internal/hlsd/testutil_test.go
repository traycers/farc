package hlsd_test

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"traycers/farc/fblock"
	"traycers/farc/internal/api"
	"traycers/farc/internal/fcontainer"
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

type farcdTestServer struct {
	*httptest.Server
	wsURL string
}

func newFarcdTestServer(t *testing.T, unit *storage.Unit) *farcdTestServer {
	t.Helper()
	reg := api.NewStorageRegistry()
	if err := reg.Register("s1", unit, "/dev/null"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := api.NewEventPushServer(reg)
	srv := api.NewHttpApiServer(reg, nil, push)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &farcdTestServer{Server: ts, wsURL: "ws" + strings.TrimPrefix(ts.URL, "http")}
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
