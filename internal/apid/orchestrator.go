package apid

import (
	"context"
	"fmt"
	"strconv"
)

// ChannelWriteRequest is Orchestrator.CreateChannel/UpdateChannel's input:
// the channel's metadata plus the camera's real RTSP URL -- the one field
// that never reaches farcd verbatim, since farcd is wired to pull the
// stream from mediamtx instead (.scratch/live-page/spec.md's "single RTSP
// connection to the camera" decision).
type ChannelWriteRequest struct {
	ID                 uint16
	CameraRTSPURL      string
	Storage            string
	CapturePolicyType  string
	MaxDeferredStartNS uint64
	PrerecordNS        uint64
	PostrecordNS       uint64
	Name               string
}

// WriteResult reports each side's outcome independently, so a caller (the
// HTTP handler in server.go) can render
// .scratch/live-page/issues/01-apid-server.md's partial-failure shape
// ({"farcd": "ok"/"error: ...", "mediamtx": "ok"/"error: ..."}) -- one
// side failing never rolls back the other (no saga/outbox exists in this
// project; see that issue file for the full rationale).
type WriteResult struct {
	FarcdOK     bool
	FarcdErr    error
	MediamtxOK  bool
	MediamtxErr error
}

// Orchestrator implements apid's channel-write orchestration: fanning
// create/update/remove out to a FarcdClient and a MediamtxClient.
type Orchestrator struct {
	farcd    FarcdClient
	mediamtx MediamtxClient

	rtspBase         string // mediamtx's RTSP re-serve base, e.g. "rtsp://mediamtx:8554"
	webrtcPublicBase string // mediamtx's browser-facing WHEP base, e.g. "http://mediamtx:8889"
}

// NewOrchestrator creates an Orchestrator. rtspBase/webrtcPublicBase come
// from apidconfig.Config's Mediamtx.RTSPBase/WebRTCPublicBase.
func NewOrchestrator(farcd FarcdClient, mediamtx MediamtxClient, rtspBase, webrtcPublicBase string) *Orchestrator {
	return &Orchestrator{farcd: farcd, mediamtx: mediamtx, rtspBase: rtspBase, webrtcPublicBase: webrtcPublicBase}
}

// pathName is the mediamtx path name (and farcd rtsp_url path segment) for
// a channel: the channel id itself, stringified -- unique by construction,
// since farcd channel ids already are (.scratch/live-page/spec.md).
func pathName(id uint16) string { return strconv.Itoa(int(id)) }

// farcdRTSPURL is the URL farcd's ingest pulls a channel's stream from:
// mediamtx's re-serve of that channel's path, never the camera directly.
func (o *Orchestrator) farcdRTSPURL(id uint16) string {
	return fmt.Sprintf("%s/%s", o.rtspBase, pathName(id))
}

// CreateChannel ensures both a farcd channel and a mediamtx path exist for
// req.ID, tolerating either side already existing (idempotent retry after
// a partial failure, per WriteResult's doc comment):
//   - farcd: a pre-flight ListChannels checks whether req.ID already
//     exists, since farcd's own POST /channels returns 409 for both
//     "already exists" and "storage full" -- the status code alone can't
//     tell those apart (internal/api/channels.go), but this check can.
//   - mediamtx: PathExists decides AddPath (fresh) vs PatchPath (already
//     there, e.g. the camera URL changed since the last attempt).
func (o *Orchestrator) CreateChannel(ctx context.Context, req ChannelWriteRequest) WriteResult {
	var result WriteResult

	existing, err := o.farcd.ListChannels(ctx)
	switch {
	case err != nil:
		result.FarcdErr = err
	case channelExists(existing, req.ID):
		result.FarcdOK = true
	default:
		_, err := o.farcd.CreateChannel(ctx, CreateChannelRequest{
			ID:                 req.ID,
			RTSPURL:            o.farcdRTSPURL(req.ID),
			Storage:            req.Storage,
			CapturePolicyType:  req.CapturePolicyType,
			MaxDeferredStartNS: req.MaxDeferredStartNS,
			PrerecordNS:        req.PrerecordNS,
			PostrecordNS:       req.PostrecordNS,
			Name:               req.Name,
		})
		if err != nil {
			result.FarcdErr = err
		} else {
			result.FarcdOK = true
		}
	}

	result.MediamtxOK, result.MediamtxErr = o.ensureMediamtxPath(ctx, req.ID, req.CameraRTSPURL)
	return result
}

// ensureMediamtxPath adds or patches (whichever applies) the mediamtx path
// for id so its source is source.
func (o *Orchestrator) ensureMediamtxPath(ctx context.Context, id uint16, source string) (bool, error) {
	name := pathName(id)
	exists, err := o.mediamtx.PathExists(ctx, name)
	if err != nil {
		return false, err
	}
	if exists {
		err := o.mediamtx.PatchPath(ctx, name, source)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	err = o.mediamtx.AddPath(ctx, name, source)
	if err != nil {
		return false, err
	}
	return true, nil
}

func channelExists(channels []ChannelInfo, id uint16) bool {
	for _, c := range channels {
		if c.Channel == id {
			return true
		}
	}
	return false
}

// UpdateChannel replaces channel id's metadata on farcd (mirroring farcd's
// own PUT /channels/{id} full-replace semantics -- there is no partial
// patch to diff against) and the mediamtx path's source, unconditionally
// on both sides rather than trying to detect "did anything actually
// change" -- simpler, and consistent with this package's idempotent,
// converge-to-the-given-state design throughout.
func (o *Orchestrator) UpdateChannel(ctx context.Context, id uint16, req ChannelWriteRequest) WriteResult {
	var result WriteResult

	_, err := o.farcd.UpdateChannel(ctx, id, UpdateChannelRequest{
		RTSPURL:            o.farcdRTSPURL(id),
		Storage:            req.Storage,
		CapturePolicyType:  req.CapturePolicyType,
		MaxDeferredStartNS: req.MaxDeferredStartNS,
		PrerecordNS:        req.PrerecordNS,
		PostrecordNS:       req.PostrecordNS,
		Name:               req.Name,
	})
	if err != nil {
		result.FarcdErr = err
	} else {
		result.FarcdOK = true
	}

	result.MediamtxOK, result.MediamtxErr = o.ensureMediamtxPath(ctx, id, req.CameraRTSPURL)
	return result
}

// RemoveChannel removes channel id from both farcd and mediamtx. Both
// clients' RemoveChannel/DeletePath are individually idempotent (treat
// "already gone" as success), so a retried RemoveChannel call is safe.
func (o *Orchestrator) RemoveChannel(ctx context.Context, id uint16) WriteResult {
	var result WriteResult

	err := o.farcd.RemoveChannel(ctx, id)
	if err != nil {
		result.FarcdErr = err
	} else {
		result.FarcdOK = true
	}

	err = o.mediamtx.DeletePath(ctx, pathName(id))
	if err != nil {
		result.MediamtxErr = err
	} else {
		result.MediamtxOK = true
	}

	return result
}

// GetCameraURL returns channel id's camera RTSP URL, read back from
// mediamtx's path config -- the only place it's stored (apid itself never
// persists it, .scratch/live-page/issues/01-apid-server.md). Used to
// prefill the web app's edit-channel form, since farcd's own rtsp_url for
// this channel is mediamtx's re-serve address, not the camera's.
func (o *Orchestrator) GetCameraURL(ctx context.Context, id uint16) (source string, exists bool, err error) {
	return o.mediamtx.GetPathSource(ctx, pathName(id))
}

// GetLiveURLs builds each id's WHEP playback URL. This never calls
// mediamtx: the URL is fully derivable from config (webrtcPublicBase) plus
// the id (mediamtx path name === channel id), so "batch" costs nothing
// here -- see .scratch/live-page/issues/01-apid-server.md.
func (o *Orchestrator) GetLiveURLs(ids []uint16) map[uint16]string {
	out := make(map[uint16]string, len(ids))
	for _, id := range ids {
		out[id] = fmt.Sprintf("%s/%s/whep", o.webrtcPublicBase, pathName(id))
	}
	return out
}
