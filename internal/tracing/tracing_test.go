package tracing_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

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

	tracing.Middleware(logf)(next).ServeHTTP(rw, req)

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

	tracing.Middleware(logf)(next).ServeHTTP(rw, req)

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
	srv := httptest.NewServer(tracing.Middleware(logf)(wsHandler))
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

	tracing.Middleware(logf)(next).ServeHTTP(rw, req)

	if !strings.Contains(logs[0], "-> 200") {
		t.Fatalf("log line = %q, want it to report status 200", logs[0])
	}
}
