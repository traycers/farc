import "@typespec/http";
import "@typespec/openapi";
import "./errors.tsp";
import "./models.tsp";
import "./storages.tsp";
import "./fcontainers.tsp";
import "./query.tsp";
import "./channels.tsp";
import "./metrics.tsp";
import "./events.tsp";

using Http;
using OpenAPI;

/**
 * farc's `internal/api` HTTP surface (PLAN.md Phase 10). No design doc under
 * docs/docs/archive/ specifies this API — PLAN.md's own "Minimal HTTP/WS/
 * metrics API" sketch was the only spec, and internal/api implements it
 * close to literally; this TypeSpec project mirrors that implementation as
 * of Phase 11 (all v1 phases complete), field for field, status code for
 * status code, comment for design-decision comment.
 *
 * Not covered here: GET /events/ws (EventPushServer) — a WebSocket endpoint,
 * which OpenAPI 3 cannot express as an operation at all. Its message shapes
 * are documented in events.tsp for reference, without a `@route`.
 */
@service(#{ title: "farc HTTP API" })
@info(#{ version: "0.1.0" })
namespace Farc;
