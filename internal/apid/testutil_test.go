package apid_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/api"
	"github.com/traycers/farc/internal/ingest"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/internal/storage"
)

// Recreated from internal/api/testutil_test.go / internal/hlsclient's own
// copy of it (an unexported _test.go helper, not importable across
// packages) -- see PLAN.md's Gap resolutions and internal/hlsclient/
// testutil_test.go's identical comment.

func smallGeometry() storage.Geometry {
	return storage.Geometry{FblockSize: 8192, N: 4, MaxChannels: 8}
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

	err := storage.CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	err = storage.Init(b, storage.InitConfig{Geometry: geo, Params: smallParams(), Now: 1})
	if err != nil {
		b.Close()
		t.Fatalf("Init: %v", err)
	}
	err = b.Close()
	if err != nil {
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

// newFarcdTestServer stands up a real farcd HTTP API server (storage "s1"
// backed by unit, plus a real IngestManager so POST/PUT/DELETE /channels
// actually work) -- the same real-server approach internal/hlsclient's own
// tests use, so apid's farcd client is exercised against real farcd
// handlers rather than a hand-rolled fake.
func newFarcdTestServer(t *testing.T, unit *storage.Unit) *httptest.Server {
	t.Helper()
	reg := api.NewStorageRegistry()
	err := reg.Register("s1", unit, "/dev/null", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	ing := ingest.NewIngestManager()
	push := api.NewEventPushServer(reg)
	srv := api.NewHttpApiServer(reg, ing, push)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}
