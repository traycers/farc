package hlsapi

import (
	"fmt"
	"net/http"

	"traycers/farc/internal/playlist"
	"traycers/farc/internal/segment"
	"traycers/farc/internal/segmentcache"
	"traycers/farc/internal/tocindex"
)

// handlePlaylist implements GET /channels/{channel}/hls/{t1}/{t2}/playlist.m3u8
// — pure read of the local tocindex.Index (ADR-018), no farcd round trip.
func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	channel, err := parseUint16(r.PathValue("channel"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t1, err := parseUint64(r.PathValue("t1"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t2, err := parseUint64(r.PathValue("t2"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if !s.channels.Has(channel) {
		writeError(w, http.StatusNotFound, fmt.Errorf("hlsapi: channel %d is not configured", channel))
		return
	}

	m3u8, err := playlist.Build(s.index, channel, t1, t2, s.targetDur)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	_, _ = w.Write([]byte(m3u8))
}

// lookupRecord resolves (channel, uuid) against the local index — the
// per-channel ChannelIndex tocindex maintains, not a farcd call.
func (s *Server) lookupRecord(channel uint16, uuid [16]byte) (tocindex.Record, bool) {
	return s.index.Channel(channel).Lookup(uuid)
}

func (s *Server) parseSegmentPathValues(w http.ResponseWriter, r *http.Request) (channel uint16, storageID string, uuid [16]byte, ok bool) {
	channel, err := parseUint16(r.PathValue("channel"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return 0, "", [16]byte{}, false
	}
	storageID = r.PathValue("storage")
	uuid, err = parseUUID(r.PathValue("uuid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return 0, "", [16]byte{}, false
	}
	return channel, storageID, uuid, true
}

// handleInit implements GET /segments/{channel}/{storage}/{uuid}/init.mp4:
// cache hit serves straight from disk; a miss builds once via
// internal/segment, caches the result, then serves it.
func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	channel, storageID, uuid, ok := s.parseSegmentPathValues(w, r)
	if !ok {
		return
	}

	key := segmentcache.InitKey(storageID, uuid)
	if data, hit := s.cache.Get(key); hit {
		writeMP4(w, data)
		return
	}

	if !s.channels.Has(channel) {
		writeError(w, http.StatusNotFound, fmt.Errorf("hlsapi: channel %d is not configured", channel))
		return
	}
	rec, ok := s.lookupRecord(channel, uuid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("hlsapi: fcontainer %x not indexed for channel %d", uuid, channel))
		return
	}
	data, err := segment.BuildInit(r.Context(), s.client, rec, channel)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.cache.Put(key, data) // best-effort: a cache write failure shouldn't fail an otherwise-successful response
	writeMP4(w, data)
}

// handleMedia implements GET /segments/{channel}/{storage}/{uuid}/{n}/seg.m4s.
// segIndex's [Start,End) bounds are recomputed from
// internal/playlist.RecordSegments rather than carried in the URL — see
// that function's doc for why this is guaranteed to match what Build
// advertised.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	channel, storageID, uuid, ok := s.parseSegmentPathValues(w, r)
	if !ok {
		return
	}
	segIndex, err := parseInt(r.PathValue("n"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	key := segmentcache.MediaKey(storageID, uuid, segIndex)
	if data, hit := s.cache.Get(key); hit {
		writeSegment(w, data)
		return
	}

	if !s.channels.Has(channel) {
		writeError(w, http.StatusNotFound, fmt.Errorf("hlsapi: channel %d is not configured", channel))
		return
	}
	rec, ok := s.lookupRecord(channel, uuid)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("hlsapi: fcontainer %x not indexed for channel %d", uuid, channel))
		return
	}
	bounds := playlist.RecordSegments(rec, channel, s.targetDur)
	if segIndex < 0 || segIndex >= len(bounds) {
		writeError(w, http.StatusNotFound, fmt.Errorf("hlsapi: segment index %d out of range (fcontainer %x has %d)", segIndex, uuid, len(bounds)))
		return
	}

	data, err := segment.BuildMedia(r.Context(), s.client, rec, channel, bounds[segIndex].Start, bounds[segIndex].End)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.cache.Put(key, data)
	writeSegment(w, data)
}

func writeMP4(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "video/mp4")
	_, _ = w.Write(data)
}

func writeSegment(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "video/iso.segment")
	_, _ = w.Write(data)
}
