import "@typespec/http";
import "./errors.tsp";
import "./models.tsp";

using Http;

namespace Farc;

/**
 * ingest.PolicyType values accepted on the wire. "schedule" parses
 * successfully but every request naming it is rejected with 501 — internal/
 * ingest has no PolicySchedule value to pass through to at all (v1's own
 * documented scope), it isn't merely unwired.
 */
enum CapturePolicyType {
  continuous: "continuous",
  event: "event",
  schedule: "schedule",
}

model CapturePolicyParams {
  prerecord_ns: uint64;
  postrecord_ns: uint64;
}

/** POST /channels/{id}/capture-policy's body. */
model SetCapturePolicyRequest {
  type: CapturePolicyType;
  params: CapturePolicyParams;
}

/**
 * POST /channels/{id}/events' body. A plain one-shot fire-and-forget POST —
 * deliberately not a long-poll, sidestepping the open question
 * docs/docs/archive/10-capture-policy.md §8 raises about that shape.
 * Omitting the body entirely (or `t`) uses the server's own wall-clock time.
 */
model TriggerEventRequest {
  t?: uint64;
}

@route("/channels/{id}")
interface Channels {
  @doc("""
    Set channel {id}'s CapturePolicy. 501 if this server has no
    IngestManager wired in, or if `type` is "schedule" (not implemented in
    v1). 404 if the channel id is not known to the IngestManager.
    """)
  @post
  @route("/capture-policy")
  setCapturePolicy(@path id: uint16, @body body: SetCapturePolicyRequest):
    | NoContent
    | BadRequestError
    | NotFoundError
    | NotImplementedError;

  @doc("""
    Fire an event-policy trigger for channel {id} (docs/docs/archive/
    10-capture-policy.md's Event capture policy). 409 if the channel's
    current CapturePolicy is not PolicyEvent (ingest.ErrWrongPolicyType);
    404 for any other failure to find/trigger the channel. 501 if this
    server has no IngestManager wired in.
    """)
  @post
  @route("/events")
  triggerEvent(@path id: uint16, @body body?: TriggerEventRequest):
    | NoContent
    | BadRequestError
    | NotFoundError
    | ConflictError
    | NotImplementedError;
}
