import "@typespec/http";
import "./models.tsp";
import "./errors.tsp";

using Http;

namespace Farc;

/**
 * POST /storages' body (internal/api/storages.go's createStorageRequest).
 * geometry/params mirror storage.Geometry/fblock.Params field-for-field —
 * there is no separate wire schema for either.
 */
model CreateStorageRequest {
  id: string;
  path: string;
  geometry: StorageGeometry;
  params: FblockParams;

  /** overwrite an existing, non-empty file at `path` instead of refusing */
  force?: boolean = false;

  /** optional SSD catalog path (ADR-007); empty means no catalog */
  catalog_path?: string = "";

  /** ioengine.Options.Backend override: "direct" | "standard" | "" (auto) */
  backend?: string = "";
}

/**
 * PATCH /storages/{id}'s body — the only two Storage params documented as
 * operator-mutable at runtime (internal/api/storages.go's
 * patchStorageRequest). Both fields are optional; only the ones present are
 * applied.
 */
model PatchStorageRequest {
  retention_days?: int64;
  write_mode?: string;
}

/**
 * StorageRegistry — the operator-facing lifecycle surface. POST /storages
 * runs Initializer and Startup synchronously in the request goroutine (ADR-
 * 006's lazy initialization is what makes this cheap enough to do inline —
 * only fblock 0 is actually written).
 */
@route("/storages")
interface Storages {
  @doc("""
    Create (or attach to) a Storage: creates/truncates the backing file to
    `geometry.FblockSize * geometry.N` bytes, runs storage.Init and
    storage.Open, then registers the resulting Unit under `id`.
    """)
  @post
  create(@body body: CreateStorageRequest):
    | {
        @statusCode statusCode: 201;
        @body body: StorageInfo;
      }
    | BadRequestError
    | ConflictError
    | InternalError;

  @doc("List every currently registered (open) Storage, sorted by id.")
  @get
  list(): StorageInfo[];

  @doc("""
    Update retention_days and/or write_mode on an already-open Storage. Both
    fields optional; omitted fields are left unchanged.
    """)
  @patch
  @route("/{id}")
  patch(@path id: string, @body body: PatchStorageRequest):
    | NoContent
    | BadRequestError
    | NotFoundError;
}
