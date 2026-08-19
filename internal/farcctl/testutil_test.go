package farcctl_test

import (
	"net/http/httptest"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/api"
	"github.com/traycers/farc/internal/ingest"
	"github.com/traycers/farc/internal/storage"
)

// Recreated from internal/api/testutil_test.go (an unexported _test.go
// helper, not importable across packages) -- same convention
// internal/hlsclient's own testutil_test.go already follows.

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

// testServer wraps a real internal/api.HttpApiServer over httptest -- farcctl
// talks to farcd's actual generic routes, so its tests exercise the real
// handlers rather than a hand-rolled fake, mirroring internal/hlsclient's own
// testServer.
type testServer struct {
	*httptest.Server
	reg *api.StorageRegistry
	ing *ingest.IngestManager
}

func newTestServer() *testServer {
	reg := api.NewStorageRegistry()
	ing := ingest.NewIngestManager()
	ing.Start(nil)
	s := api.NewHttpApiServer(reg, ing, nil)
	ts := httptest.NewServer(s.Handler())
	return &testServer{Server: ts, reg: reg, ing: ing}
}

func (ts *testServer) Close() {
	ts.ing.Stop()
	ts.Server.Close()
}
