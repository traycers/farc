// Package ingest implements IngestManager, ChannelIngest, and CapturePolicy
// (docs/docs/archive/10-capture-policy.md, 11-service-composition.md
// §5.1.1) — the RTSP ingest side of one channel: depacketizing RTP into
// access units, deciding which ones become part of a fcontainer, and
// handing a finished fcontainer to Storage's Recorder (internal/storage).
//
// # Deferred: schedule CapturePolicy
//
// 10-capture-policy.md §5.3 itself calls `schedule` "a version after
// event" and leaves its trigger-source config format unspecified (§8).
// Config validation is expected to reject `capture_policy.type ==
// "schedule"` with a clear "not implemented in v1" error — matching the
// JobRunner/GeometryManager deferral already recorded in PLAN.md. This
// package therefore only implements PolicyContinuous and PolicyEvent.
//
// # Deferred: StorageUnit -> CapturePolicy backpressure link
//
// 10-capture-policy.md §8 lists the channel connecting a Storage's
// backpressure state to a specific channel's CapturePolicy ("skip frames")
// as an explicitly open design question in the docs themselves, not
// something this implementation resolves — ChannelIngest always forwards
// every frame its Policy decides to keep.
//
// # Design note: one FrameQueue, split internally per (stream, kind)
//
// 10-capture-policy.md §2 describes "the queue" as a single per-channel
// ring buffer that happens to retain whole GOPs for video. A channel can
// carry more than one stream (e.g. video + audio), and GOP-atomic eviction
// only makes sense within one video stream's own sequence — evicting
// across an unrelated audio stream's frames by the same rule would either
// starve audio retention or leak stale video. FrameQueue therefore keeps a
// separate ordered slice per (stream, kind) internally and merges them by
// time on read (Since) — an implementation detail invisible to callers,
// who still see one logical per-channel queue.
package ingest
