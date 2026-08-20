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
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/traycers/farc/internal/levellog"
)

// HeaderRequestID and HeaderSessionID are the headers envoy sets.
const (
	HeaderRequestID = "X-Request-Id"
	HeaderSessionID = "X-Session-Id"
)

// UnmatchedPattern is the HTTPMetrics pattern label Middleware records for
// any request a *http.ServeMux couldn't route to a registered pattern (a
// genuine 404, or the mux's automatic 405 on a path that exists under a
// different method) -- see Middleware's own doc comment for why this must
// stay a fixed value rather than falling back to the raw request path.
const UnmatchedPattern = "unmatched"

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
// called, and whether the connection was hijacked out from under it.
type statusRecorder struct {
	http.ResponseWriter
	status   int
	hijacked bool
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
// implements it. A successful Hijack marks r so Middleware skips HTTPMetrics
// entirely for this request -- once hijacked, time.Since(start) measures the
// hijacked connection's whole lifetime (a WS session can run for hours), not
// one request's duration.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("tracing: underlying ResponseWriter does not implement http.Hijacker")
	}
	conn, buf, err := hj.Hijack()
	if err == nil {
		r.hijacked = true
	}
	return conn, buf, err
}

// httpDurationBuckets adds an explicit 0.3s boundary to prometheus.DefBuckets
// (which has none), so a "% of requests under 300ms" panel can read a real
// histogram bucket instead of interpolating one.
var httpDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.3, 0.5, 1, 2.5, 5, 10}

// HTTPMetrics is the pair of Prometheus collectors Middleware feeds on every
// non-hijacked request. Total's code label carries the exact status code
// (not a computed "is this an error" verdict, and not a status class) --
// deriving 4xx/5xx counts is a dashboard-query concern, not this package's.
// Both label sets stay low-cardinality by construction: method is one of a
// handful of HTTP verbs, pattern is a *http.ServeMux route pattern (bounded
// by however many routes are registered) rather than the raw request path,
// and code is bounded by the HTTP status code space.
type HTTPMetrics struct {
	Duration *prometheus.HistogramVec
	Total    *prometheus.CounterVec
}

// NewHTTPMetrics builds HTTPMetrics and registers it on reg. Callers must
// pass the same registry their own /metrics handler serves from (e.g.
// api.HttpApiServer.Registerer()) -- registering on a different or
// standalone registry would make these families silently unreachable on
// scrape.
func NewHTTPMetrics(reg prometheus.Registerer) *HTTPMetrics {
	m := &HTTPMetrics{
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds. Excludes hijacked connections (e.g. WebSocket upgrades), whose lifetime isn't one request's duration.",
			Buckets: httpDurationBuckets,
		}, []string{"method", "pattern"}),
		Total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests completed. Excludes hijacked connections, same as http_request_duration_seconds.",
		}, []string{"method", "pattern", "code"}),
	}
	reg.MustRegister(m.Duration, m.Total)
	return m
}

func (m *HTTPMetrics) observe(method, pattern string, code int, dur time.Duration) {
	m.Duration.WithLabelValues(method, pattern).Observe(dur.Seconds())
	m.Total.WithLabelValues(method, pattern, strconv.Itoa(code)).Inc()
}

// Middleware returns a net/http middleware (works on any http.Handler, not
// just a *http.ServeMux -- internal/farcd's WS event-feed server is a
// bare *api.EventPushServer, not routed through a mux) that, for each
// request, carries X-Request-Id/X-Session-Id (only the ones actually
// present -- a missing header never fabricates an id) on the request's
// context, logs one access-log line via logf after the handler returns, and
// -- if metrics is non-nil and the connection wasn't hijacked -- records
// HTTPMetrics. metrics may be nil (no metrics recorded, e.g. in tests that
// don't care about them).
//
// When next is a *http.ServeMux, the metrics' pattern label is the matched
// route pattern (via (*http.ServeMux).Handler, the same lookup next's own
// ServeHTTP does internally) rather than r.URL.Path -- this codebase's
// UUID/index/channel-keyed routes would otherwise make that label unbounded.
// (*http.ServeMux).Handler returns an EMPTY pattern for a request that
// matches no registered route (a genuine 404) or matches one only under a
// different method (the mux's automatic 405) -- both collapse onto the
// fixed UnmatchedPattern label, not r.URL.Path, or every malformed/mistyped/
// malicious request path would reopen the same unbounded-label problem.
// For any other next (the one real case being farcd's dedicated WS
// listener, which exposes exactly one route), the raw path is used as-is.
func Middleware(logf func(format string, args ...any), metrics *HTTPMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		mux, isMux := next.(*http.ServeMux)
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

			if metrics != nil && !rec.hijacked {
				pattern := r.URL.Path
				if isMux {
					pattern = UnmatchedPattern
					if _, p := mux.Handler(r); p != "" {
						pattern = p
					}
				}
				metrics.observe(r.Method, pattern, rec.status, dur)
			}

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
