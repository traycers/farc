package tocindex_test

import (
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
	return storage.Geometry{FblockSize: 8192, N: 4, MaxChannels: 8}
}

func smallParams() fblock.Params {
	return fblock.Params{
		FchunkSize:        1024,
		ReadChunkSize:     512,
		WriteMode:         fblock.WriteModeCyclic,
		Retention:         fblock.Retention{Days: 0}, // 0 == immediately eligible for reuse, so a small fixture can force eviction without simulating real elapsed days
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

func writeVideoFrame(t *testing.T, unit *storage.Unit, channels []uint16, channel uint32, begin, end uint64, frameData string, frameTime uint64, now uint64) [16]byte {
	t.Helper()
	f := fcontainer.New()
	configID, err := f.AddStreamParams(channel, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       frameTime,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   []byte{1, 2, 3},
		ParamPPS:   []byte{4, 5},
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	if err := f.AddFrames(configID, []fcontainer.Frame{
		{Data: []byte(frameData), Time: frameTime, Kind: mediatree.FrameKindI},
	}); err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	uuid, err := unit.WriteFcontainer(channels, begin, end, f, now)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}
	return uuid
}

// testServer wraps an httptest.Server exposing a single registered storage
// ("s1" backed by unit) over both HttpApiServer routes and EventPushServer's
// WS route.
type testServer struct {
	*httptest.Server
	wsURL string
}

func newTestServer(t *testing.T, unit *storage.Unit) *testServer {
	t.Helper()
	reg := api.NewStorageRegistry()
	if err := reg.Register("s1", unit, "/dev/null"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := api.NewEventPushServer(reg)
	srv := api.NewHttpApiServer(reg, nil, push)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testServer{Server: ts, wsURL: "ws" + strings.TrimPrefix(ts.URL, "http")}
}
