import "@typespec/http";

using Http;

namespace Farc;

/**
 * Every non-2xx response in this API is this exact shape — internal/api's
 * writeError always encodes `{"error": err.Error()}` verbatim, whatever the
 * status code (see internal/api/helpers.go). The concrete, status-coded
 * models below are what operations actually return; ApiError itself is only
 * the shared field shape.
 */
model ApiError {
  error: string;
}

@error
@doc("storage id, channel id or fcontainer uuid does not exist in this process")
model NotFoundError {
  ...ApiError;
  @statusCode statusCode: 404;
}

@error
@doc("malformed request body, path value or query parameter")
model BadRequestError {
  ...ApiError;
  @statusCode statusCode: 400;
}

@error
@doc("the requested id is already registered / the target is in a state that forbids this operation")
model ConflictError {
  ...ApiError;
  @statusCode statusCode: 409;
}

@error
@doc("this server has no IngestManager wired in, or the requested feature is intentionally unimplemented in v1 (e.g. capture_policy.type \"schedule\")")
model NotImplementedError {
  ...ApiError;
  @statusCode statusCode: 501;
}

@error
@doc("an unexpected internal failure (disk I/O, corruption, etc.)")
model InternalError {
  ...ApiError;
  @statusCode statusCode: 500;
}
