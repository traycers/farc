import "@typespec/http";
import "./models.tsp";
import "./errors.tsp";

using Http;

namespace Farc;

@doc("value must set to change the fblock's `protected` (read-only) flag")
model SetProtectedRequest {
  value: boolean;
}

/**
 * A fcontainer's UUIDv4, hex-encoded with no dashes (32 lowercase hex
 * chars) — matches storage.newUUIDv4's on-disk byte layout, not the
 * canonical dashed textual UUID form (internal/api/helpers.go's parseUUID).
 */
@pattern("^[0-9a-f]{32}$")
scalar fcontainerUUID extends string;

/**
 * Reads on an already-Ready fcontainer, identified by (storage id, uuid).
 * Every read here returns raw bytes (application/octet-stream), never JSON
 * — ADR-004's model is "the consumer parses the TOC itself and farcd just
 * returns bytes at given offsets"; wrapping that in JSON/base64 would only
 * add cost on the documented primary path. The one deliberate exception in
 * this whole API is GET /storages/{id}/resolve (see query.tsp), which has
 * no parsed TOC to hand its caller and so must be self-describing.
 */
@route("/storages/{id}/fcontainers/{uuid}")
interface Fcontainers {
  @doc("""
    The fcontainer's TOC section, re-encoded from the already-decoded (and
    thus already-validated) toc.Columns rather than the raw on-disk bytes.
    """)
  @get
  @route("/toc")
  readTOC(@path id: string, @path uuid: fcontainerUUID):
    | {
        @header contentType: "application/octet-stream";
        @body body: bytes;
      }
    | NotFoundError;

  @doc("""
    The fcontainer's Content section. With no `ranges` query, the whole
    section (ADR-003's full export) — this includes trailing zero padding
    out to the fblock's physical Content capacity, since CRC32Content covers
    that padded region too. With `ranges=off:len,off:len,...` (ADR-004
    §8.4), only the requested byte ranges, concatenated in request order.
    """)
  @get
  readContent(
    @path id: string,
    @path uuid: fcontainerUUID,

    @doc("comma-separated `offset:length` pairs; omit for the whole section")
    @query
    ranges?: string,
  ):
    | {
        @header contentType: "application/octet-stream";
        @body body: bytes;
      }
    | BadRequestError
    | NotFoundError
    | InternalError;

  @doc("""
    Set or clear this fblock's `protected` (read-only) catalog flag. Resolved
    by uuid -> physical index first (protected is a per-fblock catalog flag,
    not a fcontainer-identity one) — 404 if the uuid does not resolve to a
    currently-Ready fblock.
    """)
  @post
  @route("/protected")
  setProtected(
    @path id: string,
    @path uuid: fcontainerUUID,
    @body body: SetProtectedRequest,
  ): NoContent | BadRequestError | NotFoundError | ConflictError;
}
