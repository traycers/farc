// Package hlsapi is hls_server's player-facing HTTP surface: the .m3u8
// playlist route and the init/media segment routes it references, backed by
// internal/playlist, internal/segment and internal/segmentcache. It never
// talks to farcd's write path or Storage directly — only through
// internal/hlsclient, and only on a cache miss.
package hlsapi

import (
	"net/http"
	"time"

	"traycers/farc/internal/hlsclient"
	"traycers/farc/internal/segmentcache"
	"traycers/farc/internal/tocindex"
)

// Server wires internal/playlist/internal/segment/internal/segmentcache
// onto one http.ServeMux, matching internal/api/server.go's own style (no
// router library).
type Server struct {
	index     *tocindex.Index
	client    *hlsclient.Client // the one farcd this hls_server talks to (ADR-020)
	channels  map[uint16]bool   // which channels this hls_server is configured to serve
	cache     *segmentcache.Cache
	targetDur time.Duration
	mux       *http.ServeMux
}

// New builds a Server. client is the hlsclient.Client for the one farcd
// hls_server talks to (ADR-020 — v1 supports exactly one, not a per-channel
// choice); channels is the set of channel numbers this hls_server is
// configured to serve — the segment route only carries (channel, storage,
// uuid), not anything to distinguish configured from unconfigured channels
// on its own, so handlers consult this set directly (see internal/hlsd,
// which builds it from hlsconfig.Config). targetDur is the nominal segment
// duration (docs/docs/archive/12-hls-server.md's target_segment_duration) —
// the same value internal/playlist.Build and internal/playlist.
// RecordSegments must both be called with for a segment index to mean the
// same thing on both the playlist and the segment route.
func New(index *tocindex.Index, client *hlsclient.Client, channels map[uint16]bool, cache *segmentcache.Cache, targetDur time.Duration) *Server {
	s := &Server{index: index, client: client, channels: channels, cache: cache, targetDur: targetDur, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the server's http.Handler, e.g. for http.Server.Handler or
// httptest.NewServer.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /channels/{channel}/hls/{t1}/{t2}/playlist.m3u8", s.handlePlaylist)
	s.mux.HandleFunc("GET /segments/{channel}/{storage}/{uuid}/init.mp4", s.handleInit)
	s.mux.HandleFunc("GET /segments/{channel}/{storage}/{uuid}/{n}/seg.m4s", s.handleMedia)
}
