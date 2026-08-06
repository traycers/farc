package tocindex

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"traycers/farc/internal/hlsclient"
	"traycers/farc/mediatree"
	"traycers/farc/toc"
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

// EventSubscriber keeps an Index current for one storage's configured
// channels (ADR-018): push events are the steady-state path; ADR-016's
// resolve fallback is used only via bootstrap, on first connect and after
// any disconnect. Candidates(channel, 0, max) as the bootstrap query
// naturally covers "the full retention window" with no separate retention
// parameter — fblocks that fell out of retention are simply no longer
// returned by Candidates, so there's nothing stale to filter out.
type EventSubscriber struct {
	client    *hlsclient.Client
	storageID string
	channels  []uint16
	index     *Index
	logf      func(format string, args ...any)
}

// NewEventSubscriber creates a subscriber that keeps index current for
// channels on storageID, via client.
func NewEventSubscriber(client *hlsclient.Client, storageID string, channels []uint16, index *Index) *EventSubscriber {
	return &EventSubscriber{
		client:    client,
		storageID: storageID,
		channels:  channels,
		index:     index,
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
			s.logf("tocindex: bootstrap failed for storage %s, retrying: %v", s.storageID, err)
			if !sleepCtx(ctx, reconnectDelay) {
				return nil
			}
			continue
		}

		events, err := s.client.Subscribe(ctx, s.storageID, []string{EventWriteCompleted, EventDeleted}, s.channels)
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // a cancelled context is normal shutdown, not a failure to report
			}
			s.logf("tocindex: subscribe failed for storage %s, retrying: %v", s.storageID, err)
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
	for ev := range events {
		switch ev.Name {
		case EventWriteCompleted:
			s.handleWriteCompleted(ctx, ev)
		case EventDeleted:
			s.handleDeleted(ev)
		}
	}
}

// handleWriteCompleted implements the Gap resolutions entry "TOC is not
// inline with the push event": the push carries only the UUID, so a
// synchronous follow-up GetTOC is required before the container can be
// indexed.
func (s *EventSubscriber) handleWriteCompleted(ctx context.Context, ev hlsclient.Event) {
	if !ev.HasUUID {
		return
	}
	columns, err := s.client.GetTOC(ctx, s.storageID, ev.UUID)
	if err != nil {
		s.logf("tocindex: fetch toc after write.completed for storage %s uuid %x: %v", s.storageID, ev.UUID, err)
		return
	}
	s.indexContainer(ev.UUID, columns)
}

func (s *EventSubscriber) handleDeleted(ev hlsclient.Event) {
	if !ev.HasUUID {
		return
	}
	for _, ch := range s.channels {
		s.index.Channel(ch).Remove(ev.UUID)
	}
}

// bootstrap fills the index from every fblock currently on disk for each
// configured channel (see the EventSubscriber doc comment on why this alone
// covers the retention window), fetching and indexing each candidate's TOC
// exactly like a live write.completed event.
func (s *EventSubscriber) bootstrap(ctx context.Context) error {
	for _, ch := range s.channels {
		cands, err := s.client.Candidates(ctx, s.storageID, ch, 0, math.MaxUint64)
		if err != nil {
			return fmt.Errorf("tocindex: bootstrap: candidates for channel %d: %w", ch, err)
		}
		for _, cand := range cands {
			columns, err := s.client.GetTOC(ctx, s.storageID, cand.UUID)
			if err != nil {
				return fmt.Errorf("tocindex: bootstrap: toc for %x: %w", cand.UUID, err)
			}
			s.indexContainer(cand.UUID, columns)
		}
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
		s.index.Channel(ch).Insert(Record{UUID: uuid, StorageID: s.storageID, Begin: begin, End: end, Columns: columns})
	}
}

// channelTimeRange finds channel's node in columns and returns the min/max
// timestamp across every frame_time node (video or audio) in its subtree.
// Reimplements internal/api/query.go's findChannelNode locally (unexported
// there, in a different package) plus a min/max reduction over the same
// toc.ScanByRole/SubtreeRange primitives resolveChannelFrames uses.
func channelTimeRange(c *toc.Columns, channel uint16) (begin, end uint64, ok bool) {
	channelNodeID, found := findChannelNode(c, channel)
	if !found {
		return 0, 0, false
	}
	start, stop := toc.SubtreeRange(c, channelNodeID)
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

func findChannelNode(c *toc.Columns, channel uint16) (uint32, bool) {
	for _, id := range toc.ScanByRole(c, mediatree.RoleChannel) {
		v, ok := toc.InlineValue(c, id)
		if ok && len(v) == 4 && binary.LittleEndian.Uint32(v) == uint32(channel) {
			return id, true
		}
	}
	return 0, false
}
