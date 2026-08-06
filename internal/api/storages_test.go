package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func postJSON(t *testing.T, srv *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func putJSON(t *testing.T, srv *httptest.Server, path string, body any) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, srv.URL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	return resp
}

func decodeBody(resp *http.Response, v any) error {
	return json.NewDecoder(resp.Body).Decode(v)
}

func TestHandleCreateStorage_InitsOpensAndRegisters(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	imgPath := filepath.Join(t.TempDir(), "storage.img")
	req := createStorageRequest{
		ID:       "s1",
		Path:     imgPath,
		Geometry: smallGeometry(),
		Params:   smallParams(),
		Backend:  "standard",
	}
	resp := postJSON(t, srv, "/storages", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	var info StorageInfo
	err := json.NewDecoder(resp.Body).Decode(&info)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.ID != "s1" || info.Path != imgPath {
		t.Fatalf("info = %+v", info)
	}

	unit, ok := reg.Get("s1")
	if !ok {
		t.Fatalf("storage not registered")
	}
	defer unit.Close()
	if unit.Geometry() != smallGeometry() {
		t.Fatalf("geometry = %+v, want %+v", unit.Geometry(), smallGeometry())
	}
}

func TestHandleCreateStorage_CallsOnStorageCreatedHook(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)

	type call struct{ id, path, catalogPath, name string }
	var got *call
	s.SetOnStorageCreated(func(id, path, catalogPath, name string) error {
		got = &call{id, path, catalogPath, name}
		return nil
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	catalogPath := filepath.Join(t.TempDir(), "hooked.catalog")
	req := createStorageRequest{
		ID: "hooked", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard", CatalogPath: catalogPath,
	}
	resp := postJSON(t, srv, "/storages", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201 (body=%s)", resp.StatusCode, body)
	}
	if u, ok := reg.Get("hooked"); ok {
		defer u.Close()
	}

	if got == nil {
		t.Fatalf("onStorageCreated was not called")
	}
	if got.id != "hooked" || got.path != req.Path || got.catalogPath != catalogPath {
		t.Fatalf("hook called with %+v", got)
	}
}

func TestHandleCreateStorage_OnStorageCreatedErrorFailsRequestButKeepsRegistration(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	s.SetOnStorageCreated(func(id, path, catalogPath, name string) error {
		return errors.New("disk full")
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req := createStorageRequest{
		ID: "still-registered", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	}
	resp := postJSON(t, srv, "/storages", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}

	u, ok := reg.Get("still-registered")
	if !ok {
		t.Fatalf("storage should stay registered in-memory even though persistence failed")
	}
	u.Close()
}

func TestHandleCreateStorage_DuplicateIDConflicts(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req := createStorageRequest{
		ID: "dup", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	}
	resp1 := postJSON(t, srv, "/storages", req)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", resp1.StatusCode)
	}
	if u, ok := reg.Get("dup"); ok {
		defer u.Close()
	}

	req.Path = filepath.Join(t.TempDir(), "storage2.img")
	resp2 := postJSON(t, srv, "/storages", req)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", resp2.StatusCode)
	}
}

func TestHandleListStorages(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "/x/a.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/storages")
	if err != nil {
		t.Fatalf("GET /storages: %v", err)
	}
	defer resp.Body.Close()
	var list []StorageInfo
	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].ID != "a" {
		t.Fatalf("list = %+v", list)
	}
}

func TestHandlePatchStorage_RetentionDays(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "a.img", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	days := int64(7)
	buf, _ := json.Marshal(patchStorageRequest{RetentionDays: &days})
	httpReq, err := http.NewRequest(http.MethodPatch, srv.URL+"/storages/a", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if got := u.Index().RetentionDays(); got != days {
		t.Fatalf("RetentionDays = %d, want %d", got, days)
	}
}

func TestHandlePatchStorage_UnknownID(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	httpReq, _ := http.NewRequest(http.MethodPatch, srv.URL+"/storages/nope", bytes.NewReader([]byte(`{}`)))
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
