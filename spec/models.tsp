import "@typespec/http";

using Http;

namespace Farc;

/**
 * A Storage's fixed, init-time shape (docs/docs/archive/
 * 04-storage-operations.md §3.1 step 3; internal/storage/geometry.go).
 *
 * Field names are PascalCase on the wire, not snake_case like every other
 * model in this API: internal/storage.Geometry carries no `json` struct
 * tags, so encoding/json falls back to the Go field names verbatim. This is
 * a real, current quirk of the implementation (internal/api/registry.go's
 * StorageInfo and internal/api/storages.go's createStorageRequest both embed
 * this struct as-is) — not a spec typo.
 */
model StorageGeometry {
  FblockSize: uint64;
  N: uint32;
  MaxChannels: uint16;
}

/** fblock.Retention — days after an fblock's End time it stays non-writable. */
model FblockRetention {
  days: int64;
}

/**
 * fblock.Params — the JSON parameter section stored in a fblock's prologue
 * (docs/docs/archive/03-storage-format.md §5.2, ADR-009). Same shape here as
 * on disk: POST /storages takes it as-is, with no separate wire schema
 * (internal/api/storages.go's createStorageRequest doc comment).
 */
model FblockParams {
  fchunk_size: int64;
  read_chunk_size?: int64;
  write_mode: string;
  retention: FblockRetention;
  min_container_share: float64;
}

/** GET /storages' per-entry shape, and POST /storages' 201 response body. */
model StorageInfo {
  id: string;
  path: string;
  geometry: StorageGeometry;
}

/** Shared 204 response for the several endpoints that only ack. */
model NoContent {
  @statusCode statusCode: 204;
}
