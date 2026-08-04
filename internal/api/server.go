package api

import (
	"net/http"

	"traycers/farc/internal/ingest"
)

// HttpApiServer wires StorageRegistry, an optional IngestManager (channel
// routes 501 without one — see channels.go), and an optional
// EventPushServer (WS route only mounted if provided) onto one
// http.ServeMux.
type HttpApiServer struct {
	reg  *StorageRegistry
	ing  *ingest.IngestManager
	push *EventPushServer
	mux  *http.ServeMux
}

// NewHttpApiServer builds the full route set. ing and push may be nil (e.g.
// a read-only deployment, or tests exercising only the storage routes) —
// routes that need them report 501 Not Implemented rather than panicking.
func NewHttpApiServer(reg *StorageRegistry, ing *ingest.IngestManager, push *EventPushServer) *HttpApiServer {
	s := &HttpApiServer{reg: reg, ing: ing, push: push, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the server's http.Handler, e.g. for http.Server.Handler or
// httptest.NewServer.
func (s *HttpApiServer) Handler() http.Handler { return s.mux }

// MetricsHandler returns a handler serving just GET /metrics, for mounting
// on MetricsEndpoint's own address -- docs/docs/archive/
// 04-storage-operations.md §2.1's config keeps http/ws/metrics as three
// separate server addresses ("это разные серверы"), which internal/farcd
// honors by running three separate http.Server instances rather than one
// shared mux for everything. /metrics still also being reachable on the
// main API's own mux (see routes()) is harmless, not a conflict.
func (s *HttpApiServer) MetricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	return mux
}

func (s *HttpApiServer) routes() {
	s.mux.HandleFunc("POST /storages", s.handleCreateStorage)
	s.mux.HandleFunc("GET /storages", s.handleListStorages)
	s.mux.HandleFunc("PATCH /storages/{id}", s.handlePatchStorage)

	s.mux.HandleFunc("GET /storages/{id}/fcontainers/{uuid}/toc", s.handleReadTOC)
	s.mux.HandleFunc("GET /storages/{id}/fcontainers/{uuid}", s.handleReadContent)
	s.mux.HandleFunc("POST /storages/{id}/fcontainers/{uuid}/protected", s.handleSetProtected)

	s.mux.HandleFunc("GET /storages/{id}/candidates", s.handleCandidates)
	s.mux.HandleFunc("GET /storages/{id}/resolve", s.handleResolve)

	s.mux.HandleFunc("POST /channels/{id}/capture-policy", s.handleSetCapturePolicy)
	s.mux.HandleFunc("POST /channels/{id}/events", s.handleTriggerEvent)

	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	if s.push != nil {
		s.mux.HandleFunc("GET /events/ws", s.push.ServeHTTP)
	}
}
