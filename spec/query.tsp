import "@typespec/http";
import "./errors.tsp";

using Http;

namespace Farc;

/**
 * One GET .../candidates result entry — enough for a caller to fetch this
 * fcontainer's TOC (GET .../toc) and confirm the exact match itself, per
 * ADR-014's "маска сокращает число кандидатов, а не заменяет TOC" (the
 * bitmap only narrows candidates; it never replaces reading the TOC).
 */
model CandidateInfo {
  index: uint32;
  uuid: string;
  begin: uint64;
  end: uint64;
}

/**
 * One GET .../resolve result entry (ADR-016 fallback). Self-describing,
 * since the whole point of this endpoint is serving a consumer with no
 * parsed TOC of its own to interpret raw offsets against — unlike every
 * other read endpoint in this API (fcontainers.tsp), which returns raw
 * bytes instead.
 */
model ResolvedFrame {
  uuid: string;
  time: uint64;

  @doc("video frames only — one of mediatree's FrameKind* constants")
  kind?: uint8;

  @doc("base64-encoded frame payload")
  data: string;
}

@route("/storages/{id}")
interface Query {
  @doc("""
    fblock-level candidates only (ADR-014) — no TOC read, so this can return
    false positives: a channel_bitmap hit only means "some data for this
    channel might be in this fblock", not an exact match.
    """)
  @get
  @route("/candidates")
  candidates(
    @path id: string,
    @query channel: uint16,
    @query t1: uint64,
    @query t2: uint64,
  ): CandidateInfo[] | BadRequestError | NotFoundError;

  @doc("""
    ADR-016's TOC-resolve fallback: finds every candidate fblock (ADR-014),
    reads and confirms each one's TOC itself, and returns the matching
    frames' actual data. Intended for a consumer that has no parsed TOC on
    hand yet (e.g. the first request right after (re)connecting) — GET
    .../candidates + GET .../toc is the cheaper steady-state path once a
    consumer has its own TOC cache.
    
    No width limit is enforced on [t1,t2] — ADR-016 itself leaves that as an
    open question, not a decided bound, so a caller requesting a very wide
    range can make this endpoint read and return an unbounded amount of
    data.
    """)
  @get
  @route("/resolve")
  resolve(
    @path id: string,
    @query channel: uint16,
    @query t1: uint64,
    @query t2: uint64,
  ): ResolvedFrame[] | BadRequestError | NotFoundError | InternalError;
}
