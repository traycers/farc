package archivesapi

import (
	"net/http/httptest"

	"github.com/traycers/farc/internal/api"
	"github.com/traycers/farc/internal/farcctl"
	"github.com/traycers/farc/internal/ingest"
)

// newTestFarcd starts a real internal/api.HttpApiServer (the same one
// farcd itself runs) over httptest -- archivesapi is a translation layer in
// front of farcd's generic API, so its own tests exercise the real farcd
// handlers through a real farcctl.Client rather than a hand-rolled fake,
// mirroring internal/farcctl's own testServer and the original (now
// removed) internal/api/archives_test.go's full-stack style.
func newTestFarcd() *httptest.Server {
	ing := ingest.NewIngestManager()
	ing.Start(nil)
	s := api.NewHttpApiServer(api.NewStorageRegistry(), ing, nil)
	ts := httptest.NewServer(s.Handler())
	return ts
}

// newTestServer wires a farcctl.Client pointed at farcdSrv into a fresh
// archivesapi.Server, exposed over its own httptest server -- the pair a
// test drives requests through.
func newTestServer(farcdSrv *httptest.Server) *httptest.Server {
	client := farcctl.New(farcdSrv.URL)
	srv := New(client)
	return httptest.NewServer(srv.Handler())
}
