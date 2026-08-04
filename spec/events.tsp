namespace Farc;

/**
 * GET /events/ws (EventPushServer, internal/api/eventpush.go) is a
 * WebSocket endpoint and therefore cannot be expressed as an OpenAPI 3
 * operation — OpenAPI has no operation type for a bidirectional, long-lived
 * connection. It is intentionally left out of this file's `@route`/`@get`
 * surface (and so out of the compiled openapi.yaml too); the message
 * schemas below are documented here for reference only.
 *
 * Protocol: client connects, sends exactly one SubscribeMessage, then
 * receives zero or more PushMessage frames for the lifetime of the
 * connection (no reconnect catch-up in v1 — a client that misses events
 * while disconnected gets nothing for that gap; GET /storages/{id}/resolve
 * is the catch-up path for actual data, not for events).
 */
model SubscribeMessage {
  storage: string;

  @doc("event names to receive; empty means every event name")
  want: string[];

  @doc("""
    channel ids to filter by, approximately: matched against the target
    fblock's catalog channel_bitmap, which only says "might contain",
    never "exactly contains". Empty means every channel.
    """)
  channels: uint16[];
}

/** One pushed WS frame — a JSON-friendly mirror of storage.Event. */
model PushMessage {
  @doc("always \"event\" in v1 — no separate toc-change push type exists yet")
  type: "event";

  name: string;
  index?: uint32;
  uuid?: string;
  severity?: string;
  reason?: string;
}
