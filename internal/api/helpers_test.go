package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/traycers/farc/internal/ingest"
)

func TestRequireIngest_NoIngestManager_Returns501WithoutCallingHandler(t *testing.T) {
	s := NewHttpApiServer(NewStorageRegistry(), nil, nil)
	called := false
	h := s.requireIngest(func(w http.ResponseWriter, r *http.Request) { called = true })

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/channels", nil))

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
	if called {
		t.Fatal("wrapped handler was called despite s.ing == nil")
	}
}

func TestRequireIngest_WithIngestManager_CallsThrough(t *testing.T) {
	s := NewHttpApiServer(NewStorageRegistry(), ingest.NewIngestManager(), nil)
	called := false
	h := s.requireIngest(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) })

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/channels", nil))

	if !called {
		t.Fatal("wrapped handler was not called despite s.ing != nil")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (from the wrapped handler)", w.Code, http.StatusNoContent)
	}
}

func TestWriteCommandError_WrongPolicyTypeIs409(t *testing.T) {
	w := httptest.NewRecorder()
	writeCommandError(w, ingest.ErrWrongPolicyType)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestWriteCommandError_OtherErrorIs404(t *testing.T) {
	w := httptest.NewRecorder()
	writeCommandError(w, errors.New("ingest: unknown channel 99"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
