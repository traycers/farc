package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/traycers/farc/internal/ingest"
	"github.com/traycers/farc/internal/storage"
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

	type call struct {
		id, path, catalogPath, name string
		pool                        storage.PoolTuning
	}
	var got *call
	s.SetOnStorageCreated(func(id, path, catalogPath, name string, pool storage.PoolTuning) error {
		got = &call{id, path, catalogPath, name, pool}
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
	if got.pool != storage.DefaultPoolTuning() {
		t.Fatalf("hook called with pool = %+v, want resolved defaults %+v (req.Pool was left zero-valued)", got.pool, storage.DefaultPoolTuning())
	}
}

func TestHandleCreateStorage_OnStorageCreatedErrorFailsRequestButKeepsRegistration(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	s.SetOnStorageCreated(func(id, path, catalogPath, name string, pool storage.PoolTuning) error {
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

// TestHandleCreateStorage_ExplicitPoolRoundTripsThroughList proves an
// explicit pool object in the request survives Open+Register and is what a
// subsequent GET /storages reports -- not the request's raw value read back
// blindly, but resolvedPool == unit.PoolTuning() (createStorage's own doc
// comment on why: req.Pool may have zero fields storage.Open resolves).
func TestHandleCreateStorage_ExplicitPoolRoundTripsThroughList(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req := createStorageRequest{
		ID: "pooled", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
		Pool: storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8},
	}
	resp := postJSON(t, srv, "/storages", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201 (body=%s)", resp.StatusCode, body)
	}
	if u, ok := reg.Get("pooled"); ok {
		defer u.Close()
	}

	var info StorageInfo
	if err := decodeBody(resp, &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Pool != (storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}) {
		t.Fatalf("response Pool = %+v, want {8 4 8}", info.Pool)
	}

	listResp, err := http.Get(srv.URL + "/storages")
	if err != nil {
		t.Fatalf("GET /storages: %v", err)
	}
	defer listResp.Body.Close()
	var list []StorageInfo
	if err := decodeBody(listResp, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].Pool != (storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}) {
		t.Fatalf("GET /storages list = %+v, want Pool {8 4 8}", list)
	}
}

// TestHandleCreateStorage_OmittedPoolResolvesToDefaults is the assertion
// that actually distinguishes reading back unit.PoolTuning() (correct) from
// echoing req.Pool verbatim (a bug: req.Pool would be the zero value here,
// not the resolved 4/2/4).
func TestHandleCreateStorage_OmittedPoolResolvesToDefaults(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req := createStorageRequest{
		ID: "defaulted", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
		// Pool deliberately omitted (zero value).
	}
	resp := postJSON(t, srv, "/storages", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201 (body=%s)", resp.StatusCode, body)
	}
	if u, ok := reg.Get("defaulted"); ok {
		defer u.Close()
	}

	var info StorageInfo
	if err := decodeBody(resp, &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Pool != storage.DefaultPoolTuning() {
		t.Fatalf("response Pool = %+v, want resolved defaults %+v", info.Pool, storage.DefaultPoolTuning())
	}
}

// TestHandleCreateStorage_InvalidPoolOrderingRejected400 guards
// createStorage's req.Pool.Validate() check.
func TestHandleCreateStorage_InvalidPoolOrderingRejected400(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	req := createStorageRequest{
		ID: "bad-pool", Path: filepath.Join(t.TempDir(), "storage.img"),
		Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
		Pool: storage.PoolTuning{Size: 4, WarningAt: 6, BackpressureAt: 8},
	}
	resp := postJSON(t, srv, "/storages", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, body)
	}
	if _, ok := reg.Get("bad-pool"); ok {
		t.Fatalf("storage must not be registered when pool validation rejects the request")
	}
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
	err := reg.Register("a", u, "/x/a.img", "", storage.PoolTuning{})
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
	err := reg.Register("a", u, "a.img", "", storage.PoolTuning{})
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

func TestHandlePatchStorage_Pool_UpdatesRegistryImmediately(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "a.img", "", storage.PoolTuning{Size: 4, WarningAt: 2, BackpressureAt: 4})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	pool := storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}
	buf, _ := json.Marshal(patchStorageRequest{Pool: &pool})
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
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204 (body=%s)", resp.StatusCode, body)
	}

	listResp, err := http.Get(srv.URL + "/storages")
	if err != nil {
		t.Fatalf("GET /storages: %v", err)
	}
	defer listResp.Body.Close()
	var list []StorageInfo
	if err := decodeBody(listResp, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Pool != pool {
		t.Fatalf("GET /storages after PATCH = %+v, want Pool %+v reflected immediately (registry cache, no restart)", list, pool)
	}
}

// TestHandlePatchStorage_Pool_PartialGroupIsResolvedBeforeStoring guards
// against storing the raw (possibly zero-field) request: PATCH with only
// Size set must resolve WarningAt/BackpressureAt to their defaults before
// landing in the registry (and therefore GET /storages), not leave them at
// the request's own zero value -- which would misrepresent the pool actually
// in effect after farcd's next restart (config.Storage.PoolWarningAt=0
// resolves to the default there too).
func TestHandlePatchStorage_Pool_PartialGroupIsResolvedBeforeStoring(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "a.img", "", storage.PoolTuning{Size: 4, WarningAt: 2, BackpressureAt: 4})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	partial := storage.PoolTuning{Size: 8} // WarningAt/BackpressureAt left zero
	buf, _ := json.Marshal(patchStorageRequest{Pool: &partial})
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
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204 (body=%s)", resp.StatusCode, body)
	}

	want := storage.PoolTuning{Size: 8, WarningAt: 2, BackpressureAt: 4}
	if got := reg.List()[0].Pool; got != want {
		t.Fatalf("registry Pool after partial PATCH = %+v, want resolved %+v (not the raw partial request)", got, want)
	}
}

func TestHandlePatchStorage_Pool_InvalidOrderingRejected400AndLeavesPreviousValue(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	original := storage.PoolTuning{Size: 4, WarningAt: 2, BackpressureAt: 4}
	err := reg.Register("a", u, "a.img", "", original)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	bad := storage.PoolTuning{Size: 4, WarningAt: 6, BackpressureAt: 8}
	buf, _ := json.Marshal(patchStorageRequest{Pool: &bad})
	httpReq, err := http.NewRequest(http.MethodPatch, srv.URL+"/storages/a", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, body)
	}

	if got := reg.List()[0].Pool; got != original {
		t.Fatalf("registry Pool after rejected PATCH = %+v, want unchanged %+v", got, original)
	}
}

func TestHandlePatchStorage_OmittedPoolLeavesItUnchanged(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	original := storage.PoolTuning{Size: 4, WarningAt: 2, BackpressureAt: 4}
	err := reg.Register("a", u, "a.img", "", original)
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

	if got := reg.List()[0].Pool; got != original {
		t.Fatalf("registry Pool after pool-less PATCH = %+v, want unchanged %+v", got, original)
	}
}

func TestHandlePatchStorage_Pool_CallsOnStoragePoolUpdatedHook(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "a.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	var gotID string
	var gotPool storage.PoolTuning
	s.SetOnStoragePoolUpdated(func(id string, pool storage.PoolTuning) error {
		gotID, gotPool = id, pool
		return nil
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	pool := storage.PoolTuning{Size: 8, WarningAt: 4, BackpressureAt: 8}
	buf, _ := json.Marshal(patchStorageRequest{Pool: &pool})
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
	if gotID != "a" || gotPool != pool {
		t.Fatalf("onStoragePoolUpdated called with id=%q pool=%+v, want a/%+v", gotID, gotPool, pool)
	}
}

func TestHandleRemoveStorage_UnregistersAndCloses(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "a.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	httpReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/storages/a", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	if _, ok := reg.Get("a"); ok {
		t.Fatalf("storage still registered after DELETE")
	}
}

// TestHandleRemoveStorage_LeavesTheBackingFileOnDisk proves DELETE
// /storages/{id} is a detach, not a delete: it never touches the
// underlying image file, only the in-memory registration and the open fd
// (mirroring how POST /storages never creates more than that file either --
// see createStorage's own doc comment). No package under internal/storage,
// internal/storageengine, internal/ioengine, or this one calls
// os.Remove/unix.Unlink on a storage's backing file anywhere in this path.
func TestHandleRemoveStorage_LeavesTheBackingFileOnDisk(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	imgPath := filepath.Join(t.TempDir(), "storage.img")
	resp := postJSON(t, srv, "/storages", createStorageRequest{
		ID: "a", Path: imgPath, Geometry: smallGeometry(), Params: smallParams(), Backend: "standard",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	if _, err := os.Stat(imgPath); err != nil {
		t.Fatalf("backing file missing right after create: %v", err)
	}

	httpReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/storages/a", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	delResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}

	if _, err := os.Stat(imgPath); err != nil {
		t.Fatalf("backing file %s gone after DELETE /storages/a (want detach, not delete): %v", imgPath, err)
	}
}

func TestHandleRemoveStorage_RefusesWhileChannelsStillAttached(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "a.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	im := ingest.NewIngestManager()
	im.Start([]ingest.ChannelConfig{{
		Channel: 1, RTSPURL: "rtsp://127.0.0.1:1/nonexistent", StorageID: "a", SegmentBackend: fakeSegmentBackend{},
		QueueDepth: uint64(time.Second), PolicyType: ingest.PolicyContinuous,
		ReadTimeout: time.Second, WriteTimeout: time.Second,
	}})
	t.Cleanup(im.Stop)

	s := NewHttpApiServer(reg, im, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	httpReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/storages/a", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (storage still has an attached channel)", resp.StatusCode)
	}
	if _, ok := reg.Get("a"); !ok {
		t.Fatalf("storage should stay registered when removal is refused")
	}
}

func TestHandleRemoveStorage_CallsOnStorageRemovedHookBeforeClosing(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "a.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)

	var calledWith string
	var stillRegisteredWhenHookRan bool
	s.SetOnStorageRemoved(func(id string) error {
		calledWith = id
		_, stillRegisteredWhenHookRan = reg.Get(id)
		return nil
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	httpReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/storages/a", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if calledWith != "a" {
		t.Fatalf("onStorageRemoved called with %q, want %q", calledWith, "a")
	}
	if !stillRegisteredWhenHookRan {
		t.Fatalf("onStorageRemoved must run before the storage is unregistered")
	}
}

func TestHandleRemoveStorage_OnStorageRemovedErrorKeepsStorageRegistered(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	err := reg.Register("a", u, "a.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	s := NewHttpApiServer(reg, nil, nil)
	s.SetOnStorageRemoved(func(id string) error {
		return errors.New("persist failed")
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	httpReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/storages/a", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if _, ok := reg.Get("a"); !ok {
		t.Fatalf("storage should stay registered when persisting its removal fails")
	}
}

func TestHandleRemoveStorage_UnknownID(t *testing.T) {
	reg := NewStorageRegistry()
	s := NewHttpApiServer(reg, nil, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	httpReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/storages/nope", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
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
