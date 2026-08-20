package tracing_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/traycers/farc/internal/tracing"
)

func TestMiddleware_HeadersPresent_PropagatedAndLogged(t *testing.T) {
	var gotReqID, gotSessID string
	var reqIDOK, sessIDOK bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReqID, reqIDOK = tracing.RequestID(r.Context())
		gotSessID, sessIDOK = tracing.SessionID(r.Context())
		w.WriteHeader(http.StatusCreated)
	})

	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	req.Header.Set(tracing.HeaderRequestID, "req-1")
	req.Header.Set(tracing.HeaderSessionID, "sess-1")
	rw := httptest.NewRecorder()

	tracing.Middleware(logf, nil)(next).ServeHTTP(rw, req)

	if !reqIDOK || gotReqID != "req-1" {
		t.Fatalf("RequestID in handler = (%q, %v), want (\"req-1\", true)", gotReqID, reqIDOK)
	}
	if !sessIDOK || gotSessID != "sess-1" {
		t.Fatalf("SessionID in handler = (%q, %v), want (\"sess-1\", true)", gotSessID, sessIDOK)
	}
	if rw.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusCreated)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %v, want exactly 1 line", logs)
	}
	if !strings.Contains(logs[0], "request_id=req-1") || !strings.Contains(logs[0], "session_id=sess-1") {
		t.Fatalf("log line = %q, want it to contain request_id=req-1 and session_id=sess-1", logs[0])
	}
}

func TestMiddleware_HeadersAbsent_NotFabricatedNotLogged(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := tracing.RequestID(r.Context()); ok {
			t.Error("RequestID present in context despite no header")
		}
		if _, ok := tracing.SessionID(r.Context()); ok {
			t.Error("SessionID present in context despite no header")
		}
	})

	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	rw := httptest.NewRecorder()

	tracing.Middleware(logf, nil)(next).ServeHTTP(rw, req)

	if len(logs) != 1 {
		t.Fatalf("logs = %v, want exactly 1 line", logs)
	}
	if strings.Contains(logs[0], "request_id=") || strings.Contains(logs[0], "session_id=") {
		t.Fatalf("log line = %q, want no request_id/session_id fields", logs[0])
	}
}

// TestMiddleware_PreservesHijack guards against internal/api.EventPushServer's
// WS upgrade breaking underneath the middleware -- httptest.NewRecorder
// doesn't implement Hijacker, so this needs a real listener.
func TestMiddleware_PreservesHijack(t *testing.T) {
	var upgrader websocket.Upgrader
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer conn.Close()
	})

	logf := func(string, ...any) {}
	srv := httptest.NewServer(tracing.Middleware(logf, nil)(wsHandler))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
}

func TestMiddleware_DefaultStatusIsOK(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok")) // never calls WriteHeader explicitly
	})

	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	rw := httptest.NewRecorder()

	tracing.Middleware(logf, nil)(next).ServeHTTP(rw, req)

	if !strings.Contains(logs[0], "-> 200") {
		t.Fatalf("log line = %q, want it to report status 200", logs[0])
	}
}

func noopLogf(string, ...any) {}

// TestMiddleware_RecordsRequestMetrics_UsesMuxPattern guards against the raw
// request path ever becoming the metrics label: internal/api's and
// internal/hlsapi's routes are keyed by UUID/index/channel, so a raw-path
// label would be unbounded on the busiest routes in the system. When next is
// a *http.ServeMux, the matched route pattern (e.g. "GET /storages/{id}")
// must be used instead of r.URL.Path.
func TestMiddleware_RecordsRequestMetrics_UsesMuxPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /storages/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	reg := prometheus.NewRegistry()
	metrics := tracing.NewHTTPMetrics(reg)

	req := httptest.NewRequest(http.MethodGet, "/storages/abc-123", nil)
	rw := httptest.NewRecorder()
	tracing.Middleware(noopLogf, metrics)(mux).ServeHTTP(rw, req)

	got := testutil.ToFloat64(metrics.Total.WithLabelValues("GET", "GET /storages/{id}", "200"))
	if got != 1 {
		t.Fatalf("http_requests_total{method=GET,pattern=\"GET /storages/{id}\",code=200} = %v, want 1", got)
	}
	if count := testutil.ToFloat64(metrics.Total.WithLabelValues("GET", "/storages/abc-123", "200")); count != 0 {
		t.Fatalf("http_requests_total observed under the raw request path (unbounded label) = %v, want 0", count)
	}
}

// TestMiddleware_MuxUnmatchedRequest_UsesFixedPattern covers a real gap the
// mux-pattern lookup above doesn't: for a request whose path matches no
// registered pattern (a genuine 404) OR matches one only under a different
// method (net/http.ServeMux's automatic 405), (*http.ServeMux).Handler
// returns an EMPTY pattern -- confirmed against a live farc container
// (GET /storages/does-not-exist, 405 because PATCH/DELETE /storages/{id}
// exist): falling back to r.URL.Path there reintroduces the exact unbounded
// label this whole pattern-lookup exists to avoid, on every malformed,
// mistyped, or malicious request path. Both cases must collapse onto one
// fixed, bounded label instead.
func TestMiddleware_MuxUnmatchedRequest_UsesFixedPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /storages/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("DELETE /storages/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	reg := prometheus.NewRegistry()
	metrics := tracing.NewHTTPMetrics(reg)

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"method mismatch (405)", httptest.NewRequest(http.MethodGet, "/storages/abc-123", nil)},
		{"no route at all (404)", httptest.NewRequest(http.MethodGet, "/does/not/exist/at/all", nil)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rw := httptest.NewRecorder()
			tracing.Middleware(noopLogf, metrics)(mux).ServeHTTP(rw, c.req)

			if got := testutil.ToFloat64(metrics.Total.WithLabelValues(http.MethodGet, tracing.UnmatchedPattern, itoa(rw.Code))); got != 1 {
				t.Fatalf("http_requests_total{pattern=%q} = %v, want 1 (code=%d)", tracing.UnmatchedPattern, got, rw.Code)
			}
			if got := testutil.ToFloat64(metrics.Total.WithLabelValues(http.MethodGet, c.req.URL.Path, itoa(rw.Code))); got != 0 {
				t.Fatalf("http_requests_total observed under the raw request path (unbounded label) = %v, want 0", got)
			}
		})
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// TestMiddleware_HijackedConnectionSkipsMetrics guards the latency histogram
// against a WebSocket upgrade's whole connection lifetime landing in it as
// one "request duration" sample -- a hijacked connection must record no
// metrics at all, on either family.
func TestMiddleware_HijackedConnectionSkipsMetrics(t *testing.T) {
	var upgrader websocket.Upgrader
	wsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer conn.Close()
	})

	reg := prometheus.NewRegistry()
	metrics := tracing.NewHTTPMetrics(reg)

	srv := httptest.NewServer(tracing.Middleware(noopLogf, metrics)(wsHandler))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	defer resp.Body.Close()

	if count := testutil.CollectAndCount(metrics.Total); count != 0 {
		t.Fatalf("http_requests_total samples after a hijacked connection = %d, want 0", count)
	}
	if count := testutil.CollectAndCount(metrics.Duration); count != 0 {
		t.Fatalf("http_request_duration_seconds samples after a hijacked connection = %d, want 0", count)
	}
}

// TestMiddleware_NonMuxHandler_FallsBackToRequestPath covers
// internal/api.EventPushServer's own dedicated WS listener (farcd's f.wsSrv):
// next there is a bare http.Handler, not a *http.ServeMux, so there's no
// route pattern to look up -- the raw path is used as-is, which is safe only
// because that server exposes exactly one route.
func TestMiddleware_NonMuxHandler_FallsBackToRequestPath(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	reg := prometheus.NewRegistry()
	metrics := tracing.NewHTTPMetrics(reg)

	req := httptest.NewRequest(http.MethodGet, "/events/ws", nil)
	rw := httptest.NewRecorder()
	tracing.Middleware(noopLogf, metrics)(next).ServeHTTP(rw, req)

	got := testutil.ToFloat64(metrics.Total.WithLabelValues("GET", "/events/ws", "400"))
	if got != 1 {
		t.Fatalf("http_requests_total{method=GET,pattern=/events/ws,code=400} = %v, want 1", got)
	}
}

// TestMiddleware_NilMetrics_NoPanic confirms passing a nil *HTTPMetrics (the
// existing tests above it in this file all do) never touches it.
func TestMiddleware_NilMetrics_NoPanic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	rw := httptest.NewRecorder()
	tracing.Middleware(noopLogf, nil)(next).ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rw.Code, http.StatusOK)
	}
}
