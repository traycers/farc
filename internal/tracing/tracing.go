// Package tracing reads the X-Request-Id/X-Session-Id headers envoy (sitting
// in front of the whole system) attaches to inbound requests, carries them on
// the request's context.Context, and logs one access-log line per request
// through the caller's existing logf callback -- the same
// func(format string, args ...any) signature every long-lived farc component
// already uses (internal/farcd.Farcd, internal/hlsd.Hlsd, ...), so plugging
// this in needs no changes to how the rest of the codebase logs.
package tracing

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/traycers/farc/internal/levellog"
)

// HeaderRequestID and HeaderSessionID are the headers envoy sets.
const (
	HeaderRequestID = "X-Request-Id"
	HeaderSessionID = "X-Session-Id"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	sessionIDKey
)

// RequestID returns the request id carried on ctx, if the inbound request
// had an X-Request-Id header.
func RequestID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(requestIDKey).(string)
	return v, ok
}

// SessionID returns the session id carried on ctx, if the inbound request
// had an X-Session-Id header.
func SessionID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(sessionIDKey).(string)
	return v, ok
}

// statusRecorder captures the status code a handler wrote, defaulting to 200
// per http.ResponseWriter's own documented behavior when WriteHeader is never
// called.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Hijack forwards to the wrapped ResponseWriter's own Hijack -- required for
// internal/api.EventPushServer's WS upgrade (gorilla/websocket.Upgrade calls
// http.ResponseWriter.(http.Hijacker)), which would otherwise fail because
// embedding the http.ResponseWriter interface alone does not promote
// Hijacker, a separate interface, even when the concrete writer underneath
// implements it.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("tracing: underlying ResponseWriter does not implement http.Hijacker")
	}
	return hj.Hijack()
}

// Middleware returns a net/http middleware (works on any http.Handler, not
// just a gorilla/mux Router -- internal/farcd's WS event-feed server is a
// bare *api.EventPushServer, not routed through mux) that, for each request,
// carries X-Request-Id/X-Session-Id (only the ones actually present -- a
// missing header never fabricates an id) on the request's context and logs
// one access-log line via logf after the handler returns.
func Middleware(logf func(format string, args ...any)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			reqID := r.Header.Get(HeaderRequestID)
			if reqID != "" {
				ctx = context.WithValue(ctx, requestIDKey, reqID)
			}
			sessID := r.Header.Get(HeaderSessionID)
			if sessID != "" {
				ctx = context.WithValue(ctx, sessionIDKey, sessID)
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r.WithContext(ctx))
			dur := time.Since(start)

			ids := ""
			if reqID != "" {
				ids += " request_id=" + reqID
			}
			if sessID != "" {
				ids += " session_id=" + sessID
			}
			levellog.New(logf).Info("http: %s %s -> %d (%s)%s", r.Method, r.URL.Path, rec.status, dur, ids)
		})
	}
}
