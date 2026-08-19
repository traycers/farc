package tocindex

import (
	"context"
	"fmt"
	"time"

	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/levellog"
	"github.com/traycers/farc/internal/toccache"
	"github.com/traycers/farc/mediatree"
	"github.com/traycers/farc/toc"
)

// Event names mirroring internal/storage.EventFblockWriteCompleted/
// EventFblockDeleted — tocindex only needs these two string literals, not a
// dependency on internal/storage (hlsclient.Event.Name is already a
// decoupled string, per its own package doc).
const (
	EventWriteCompleted = "fblock.write.completed"
	EventDeleted        = "fblock.deleted"
)

// reconnectDelay bounds how fast EventSubscriber retries a failed
// bootstrap/dial — a fixed backoff is enough for v1 (no exponential
// scheme), since farcd is expected to be reachable almost all the time.
const reconnectDelay = 2 * time.Second

// tocPushTimeout bounds how long handleWriteCompleted waits for the "toc"
// frame that should immediately follow a fblock.write.completed event
// (issue .scratch/hls-toc-bootstrap/issues/01-toc-via-ws-push.md).
// ServeHTTP's per-storage loop writes both synchronously, back to back,
// before it ever pulls its next NotificationBus event, so in the normal
// case the toc frame is already in flight essentially immediately -- this
// timeout only ever fires when no toc frame is coming at all
// (buildTOCPushMessageForUnit's own failure case server-side, e.g. the
// fblock was already recycled past retention by push time), not as a race
// against a slow-to-arrive one.
const tocPushTimeout = 2 * time.Second

// EventSubscriber keeps an Index current for one storage's configured
// channels (ADR-018): push events are the steady-state path; ADR-016's
// resolve fallback is used only via bootstrap, on first connect and after
// any disconnect. Candidates(channel, 0, max) as the bootstrap query
// naturally covers "the full retention window" with no separate retention
// parameter — fblocks that fell out of retention are simply no longer
// returned by Candidates, so there's nothing stale to filter out.
type EventSubscriber struct {
	client    hlsclient.API
	storageID string
	channels  []uint16
	index     *Index
	cache     *toccache.Cache
	logf      func(format string, args ...any)
}

// NewEventSubscriber creates a subscriber that keeps index current for
// channels on storageID, via client. cache is hls_server's persistent
// on-disk TOC store (issue
// .scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md):
// bootstrap diffs against it instead of refetching every retained
// fcontainer's TOC on every (re)connect, and every TOC this subscriber
// indexes -- live-pushed or fetched -- is written back into it so a later
// restart starts warm.
func NewEventSubscriber(client hlsclient.API, storageID string, channels []uint16, index *Index, cache *toccache.Cache) *EventSubscriber {
	return &EventSubscriber{
		client:    client,
		storageID: storageID,
		channels:  channels,
		index:     index,
		cache:     cache,
		logf:      func(string, ...any) {},
	}
}

// SetLogger sets a callback for non-fatal diagnostics, matching the rest of
// the project's logging convention (internal/ingest, internal/farcd).
func (s *EventSubscriber) SetLogger(logf func(format string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s.logf = logf
}

// Run bootstraps the index and then follows push events until ctx is
// cancelled, transparently bootstrapping again and resubscribing after any
// disconnect (dial failure, subscribe failure, or the events channel
// closing). It only returns once ctx is done.
func (s *EventSubscriber) Run(ctx context.Context) error {
	for {
		err := s.bootstrap(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // a cancelled context is normal shutdown, not a failure to report
			}
			levellog.New(s.logf).Warn("tocindex: bootstrap failed for storage %s, retrying: %v", s.storageID, err)
			if !sleepCtx(ctx, reconnectDelay) {
				return nil
			}
			continue
		}

		events, err := s.client.Subscribe(ctx, s.storageID, []string{EventWriteCompleted, EventDeleted}, s.channels, true)
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // a cancelled context is normal shutdown, not a failure to report
			}
			levellog.New(s.logf).Warn("tocindex: subscribe failed for storage %s, retrying: %v", s.storageID, err)
			if !sleepCtx(ctx, reconnectDelay) {
				return nil
			}
			continue
		}

		s.follow(ctx, events)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // a cancelled context is normal shutdown, not a failure to report
		}
		// events closed (server disconnect) -- loop back to bootstrap+resubscribe.
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *EventSubscriber) follow(ctx context.Context, events <-chan hlsclient.Event) {
	r := &eventReader{events: events}
	for {
		ev, ok := r.next(ctx)
		if !ok {
			return
		}
		switch ev.Name {
		case EventWriteCompleted:
			s.handleWriteCompleted(ctx, ev, r)
		case EventDeleted:
			s.handleDeleted(ev)
		}
	}
}

// eventReader wraps a hlsclient.Event channel with a one-slot pushback
// buffer. handleWriteCompleted's search for a fblock.write.completed
// event's paired "toc" frame consumes exactly one message looking for it;
// when that message turns out to be something else entirely (the server
// had no toc frame to send), it must still reach follow's normal dispatch
// rather than being discarded, hence the pushback.
type eventReader struct {
	events   <-chan hlsclient.Event
	buffered *hlsclient.Event
}

func (r *eventReader) next(ctx context.Context) (hlsclient.Event, bool) {
	if r.buffered != nil {
		ev := *r.buffered
		r.buffered = nil
		return ev, true
	}
	select {
	case ev, ok := <-r.events:
		return ev, ok
	case <-ctx.Done():
		return hlsclient.Event{}, false
	}
}

// tryNextTOC looks for uuid's paired "toc" frame among the next message
// available within timeout. A message that doesn't match (wrong type/uuid,
// empty payload, or none arrived in time) is pushed back for next to
// return later -- see eventReader's own doc comment on why it must not be
// dropped.
func (r *eventReader) tryNextTOC(ctx context.Context, uuid [16]byte, timeout time.Duration) ([]byte, bool) {
	ev, ok := func() (hlsclient.Event, bool) {
		if r.buffered != nil {
			ev := *r.buffered
			r.buffered = nil
			return ev, true
		}
		select {
		case ev, ok := <-r.events:
			return ev, ok
		case <-ctx.Done():
			return hlsclient.Event{}, false
		case <-time.After(timeout):
			return hlsclient.Event{}, false
		}
	}()
	if !ok {
		return nil, false
	}
	if ev.Type == "toc" && ev.UUID == uuid && len(ev.TOC) > 0 {
		return ev.TOC, true
	}
	r.buffered = &ev
	return nil, false
}

// handleWriteCompleted indexes ev's fcontainer using the "toc" frame the
// server pushes right after a fblock.write.completed event
// (EventSubscriber always subscribes with includeTOC=true), falling back to
// a synchronous GetTOC -- the only path this had before issue
// .scratch/hls-toc-bootstrap/issues/01-toc-via-ws-push.md -- when no toc
// frame arrives (tryNextTOC's own doc comment) or it fails to decode.
func (s *EventSubscriber) handleWriteCompleted(ctx context.Context, ev hlsclient.Event, r *eventReader) {
	if !ev.HasUUID {
		return
	}
	if buf, ok := r.tryNextTOC(ctx, ev.UUID, tocPushTimeout); ok {
		columns, err := toc.Decode(buf)
		if err == nil {
			s.indexContainer(ev.UUID, columns)
			putErr := s.cache.Put(s.storageID, ev.UUID, buf)
			if putErr != nil {
				levellog.New(s.logf).Warn("tocindex: cache pushed toc for storage %s uuid %x: %v", s.storageID, ev.UUID, putErr)
			}
			return
		}
		levellog.New(s.logf).Warn("tocindex: decode pushed toc for storage %s uuid %x: %v", s.storageID, ev.UUID, err)
	}
	columns, err := s.client.GetTOC(ctx, s.storageID, ev.UUID)
	if err != nil {
		levellog.New(s.logf).Warn("tocindex: fetch toc after write.completed for storage %s uuid %x: %v", s.storageID, ev.UUID, err)
		return
	}
	s.indexAndCache(ev.UUID, columns)
}

// indexAndCache is indexContainer plus persisting columns into the on-disk
// cache re-encoded -- the path used whenever this subscriber itself called
// GetTOC (bootstrap's delta, handleWriteCompleted's fallback), as opposed
// to a pushed "toc" frame, which is already raw bytes and cached as-is by
// its own caller.
func (s *EventSubscriber) indexAndCache(uuid [16]byte, columns *toc.Columns) {
	s.indexContainer(uuid, columns)
	buf, err := toc.Encode(columns)
	if err != nil {
		levellog.New(s.logf).Warn("tocindex: encode toc for cache, storage %s uuid %x: %v", s.storageID, uuid, err)
		return
	}
	putErr := s.cache.Put(s.storageID, uuid, buf)
	if putErr != nil {
		levellog.New(s.logf).Warn("tocindex: cache toc for storage %s uuid %x: %v", s.storageID, uuid, putErr)
	}
}

func (s *EventSubscriber) handleDeleted(ev hlsclient.Event) {
	if !ev.HasUUID {
		return
	}
	for _, ch := range s.channels {
		s.index.Channel(ch).Remove(ev.UUID)
	}
}

// bootstrap fills the index from every Ready fblock currently in farcd's
// catalog (see the EventSubscriber doc comment on why the full catalog
// alone covers the retention window), diffing against the on-disk cache
// instead of a GetTOC per fblock (issue
// .scratch/hls-toc-bootstrap/issues/02-persistent-toc-cache-and-catalog-diff.md):
// a uuid already cached and still Ready loads straight off disk; anything
// else costs exactly one GetTOC. A cached uuid no longer Ready in the
// catalog (aged out / overwritten by the cyclic writer between restarts) is
// evicted rather than served stale.
func (s *EventSubscriber) bootstrap(ctx context.Context) error {
	entries, err := s.client.Catalog(ctx, s.storageID)
	if err != nil {
		return fmt.Errorf("tocindex: bootstrap: catalog: %w", err)
	}
	var zeroUUID [16]byte
	live := make(map[[16]byte]bool, len(entries))
	for _, e := range entries {
		// A Ready fblock can still hold no real fcontainer: fblock 0 is
		// marked Ready in the runtime catalog the instant Init succeeds
		// (internal/storage/init.go, matching 02-storage.md §5), before
		// anything is ever written into it. Candidates' own channel-bitmap
		// filter excludes this implicitly; the bulk catalog carries no
		// bitmap (decided 2026-08-13: unfiltered), so a zero uuid is this
		// diff's own signal for "not a real fcontainer" instead.
		if e.State != "ready" || e.UUID == zeroUUID {
			continue
		}
		live[e.UUID] = true
	}

	cached, err := s.cache.List(s.storageID)
	if err != nil {
		return fmt.Errorf("tocindex: bootstrap: list cache: %w", err)
	}
	for _, uuid := range cached {
		if !live[uuid] {
			s.cache.Delete(s.storageID, uuid)
		}
	}

	for uuid := range live {
		if buf, ok := s.cache.Get(s.storageID, uuid); ok {
			columns, err := toc.Decode(buf)
			if err == nil {
				s.indexContainer(uuid, columns)
				continue
			}
			levellog.New(s.logf).Warn("tocindex: bootstrap: decode cached toc for storage %s uuid %x: %v", s.storageID, uuid, err)
			s.cache.Delete(s.storageID, uuid)
		}
		columns, err := s.client.GetTOC(ctx, s.storageID, uuid)
		if err != nil {
			return fmt.Errorf("tocindex: bootstrap: toc for %x: %w", uuid, err)
		}
		s.indexAndCache(uuid, columns)
	}
	return nil
}

// indexContainer inserts one record per configured channel actually present
// in columns, with Begin/End derived from that channel's own frame_time
// range rather than fblock-level bounds — the precise bound that matters
// once a single fblock's channels []uint16 write spans more than one
// channel (WriteFcontainer's own channels parameter allows it).
func (s *EventSubscriber) indexContainer(uuid [16]byte, columns *toc.Columns) {
	for _, ch := range s.channels {
		begin, end, ok := channelTimeRange(columns, ch)
		if !ok {
			continue
		}
		s.index.Channel(ch).Insert(Record{
			UUID:          uuid,
			StorageID:     s.storageID,
			Begin:         begin,
			End:           end,
			Columns:       columns,
			VideoSegments: VideoPresenceSegments(columns, ch),
		})
	}
}

// channelTimeRange finds channel's node in columns and returns the min/max
// timestamp across every frame_time node (video or audio) in its subtree.
func channelTimeRange(c *toc.Columns, channel uint16) (begin, end uint64, ok bool) {
	start, stop, found := toc.ChannelSubtreeRange(c, channel)
	if !found {
		return 0, 0, false
	}
	timeIDs := toc.InRange(toc.ScanByRole(c, mediatree.FrameTimeRoles...), start, stop)
	if len(timeIDs) == 0 {
		return 0, 0, false
	}
	begin, end = c.ValueOrOffset[timeIDs[0]], c.ValueOrOffset[timeIDs[0]]
	for _, id := range timeIDs[1:] {
		t := c.ValueOrOffset[id]
		if t < begin {
			begin = t
		}
		if t > end {
			end = t
		}
	}
	return begin, end, true
}
