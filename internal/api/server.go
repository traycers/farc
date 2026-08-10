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

	onStorageCreated func(id, path, catalogPath, name string) error
	onStorageUpdated func(id, name string) error
	onStorageRemoved func(id string) error
	onChannelCreated func(spec ChannelSpec) error
	onChannelUpdated func(spec ChannelSpec) error
	onChannelRemoved func(id uint16) error
}

// NewHttpApiServer builds the full route set. ing and push may be nil (e.g.
// a read-only deployment, or tests exercising only the storage routes) —
// routes that need them report 501 Not Implemented rather than panicking.
func NewHttpApiServer(reg *StorageRegistry, ing *ingest.IngestManager, push *EventPushServer) *HttpApiServer {
	s := &HttpApiServer{
		reg: reg, ing: ing, push: push, mux: http.NewServeMux(),
		onStorageCreated: func(string, string, string, string) error { return nil },
		onStorageUpdated: func(string, string) error { return nil },
		onStorageRemoved: func(string) error { return nil },
		onChannelCreated: func(ChannelSpec) error { return nil },
		onChannelUpdated: func(ChannelSpec) error { return nil },
		onChannelRemoved: func(uint16) error { return nil },
	}
	s.routes()
	return s
}

// SetOnStorageCreated installs a hook run synchronously by POST /storages,
// after the new storage is initialized and registered but before the
// response is written — internal/farcd uses this to persist the entry into
// its own config file, so it survives a process restart (PLAN.md's Gap 3
// otherwise leaves a storage created at runtime in-memory-only). A returned
// error fails the request with 500, even though the storage stays
// registered and usable for this process's lifetime — persistence failing
// doesn't undo an already-completed (and possibly expensive) Init. A nil fn
// restores the default no-op, matching this package's original behavior.
func (s *HttpApiServer) SetOnStorageCreated(fn func(id, path, catalogPath, name string) error) {
	if fn == nil {
		fn = func(string, string, string, string) error { return nil }
	}
	s.onStorageCreated = fn
}

// SetOnStorageUpdated installs a hook run synchronously by PATCH
// /storages/{id} when the request includes a name change, after the
// registry's own in-memory rename succeeds but before the response is
// written -- mirrors SetOnStorageCreated's role, keeping farcd's config file
// in sync with a renamed storage. A nil fn restores the default no-op.
func (s *HttpApiServer) SetOnStorageUpdated(fn func(id, name string) error) {
	if fn == nil {
		fn = func(string, string) error { return nil }
	}
	s.onStorageUpdated = fn
}

// SetOnStorageRemoved installs a hook run synchronously by
// archives.go's archives_detach, before the Storage is actually unregistered
// and closed -- internal/farcd uses this to stop that Storage's fblock-event
// bridge goroutine and remove its config-file entry (and those of its
// channels), so a failure to persist the removal leaves the Storage fully
// intact and running rather than half-torn-down. A nil fn restores the
// default no-op.
func (s *HttpApiServer) SetOnStorageRemoved(fn func(id string) error) {
	if fn == nil {
		fn = func(string) error { return nil }
	}
	s.onStorageRemoved = fn
}

// SetOnChannelCreated/SetOnChannelUpdated/SetOnChannelRemoved install hooks
// run synchronously by POST/PUT/DELETE /channels(/{id}), after the in-memory
// IngestManager mutation succeeds but before the response is written --
// internal/farcd uses these to keep its own config file in sync (mirroring
// SetOnStorageCreated's role for storages). A returned error fails the
// request with 500 and rolls the IngestManager mutation back (see each
// handler in channels.go), so a failed persist never leaves a channel
// silently running/stopped out of step with what's on disk. A nil fn
// restores the default no-op.
func (s *HttpApiServer) SetOnChannelCreated(fn func(spec ChannelSpec) error) {
	if fn == nil {
		fn = func(ChannelSpec) error { return nil }
	}
	s.onChannelCreated = fn
}

func (s *HttpApiServer) SetOnChannelUpdated(fn func(spec ChannelSpec) error) {
	if fn == nil {
		fn = func(ChannelSpec) error { return nil }
	}
	s.onChannelUpdated = fn
}

func (s *HttpApiServer) SetOnChannelRemoved(fn func(id uint16) error) {
	if fn == nil {
		fn = func(uint16) error { return nil }
	}
	s.onChannelRemoved = fn
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
	s.mux.HandleFunc("GET /storages/{id}/fcontainers/{uuid}/tree", s.handleReadTree)
	s.mux.HandleFunc("GET /storages/{id}/fcontainers/{uuid}", s.handleReadContent)
	s.mux.HandleFunc("POST /storages/{id}/fcontainers/{uuid}/protected", s.handleSetProtected)

	s.mux.HandleFunc("GET /storages/{id}/fblocks", s.handleListFblocks)
	s.mux.HandleFunc("GET /storages/{id}/candidates", s.handleCandidates)
	s.mux.HandleFunc("GET /storages/{id}/resolve", s.handleResolve)

	s.mux.HandleFunc("GET /channels", s.handleListChannels)
	s.mux.HandleFunc("POST /channels", s.handleCreateChannel)
	s.mux.HandleFunc("PUT /channels/{id}", s.handleUpdateChannel)
	s.mux.HandleFunc("DELETE /channels/{id}", s.handleRemoveChannel)
	s.mux.HandleFunc("POST /channels/{id}/capture-policy", s.handleSetCapturePolicy)
	s.mux.HandleFunc("POST /channels/{id}/events", s.handleTriggerEvent)
	s.mux.HandleFunc("POST /channels/{id}/recording/start", s.handleStartRecording)
	s.mux.HandleFunc("POST /channels/{id}/recording/stop", s.handleStopRecording)

	s.mux.HandleFunc("GET /metrics", s.handleMetrics)

	s.mux.HandleFunc("PUT /api/v1/archives/", s.handleArchiveSetup)
	s.mux.HandleFunc("DELETE /api/v1/archives/", s.handleArchiveDetach)
	s.mux.HandleFunc("POST /api/v1/archives/{aid}/channels/", s.handleArchiveChannelsAdd)
	s.mux.HandleFunc("DELETE /api/v1/archives/{aid}/channels/", s.handleArchiveChannelsDel)
	s.mux.HandleFunc("PATCH /api/v1/archives/{aid}/channels/config/", s.handleArchiveChannelsConfigUpdate)
	s.mux.HandleFunc("POST /api/v1/archives/{aid}/recording/start", s.handleArchiveRecordingStart)
	s.mux.HandleFunc("POST /api/v1/archives/{aid}/recording/stop", s.handleArchiveRecordingStop)
	s.mux.HandleFunc("PUT /api/v1/archives/{aid}/ttl/", s.handleArchiveTTLSet)

	if s.push != nil {
		s.mux.HandleFunc("GET /events/ws", s.push.ServeHTTP)
	}
}
