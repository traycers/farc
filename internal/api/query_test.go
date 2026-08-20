package api

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/mediatree"
)

func TestHandleCandidates(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	uuid1 := writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	writeVideoFrame(t, u, []uint16{2}, 2, 300, 400, "frame-b", 300, 2000)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/s1/candidates?channel=1&t1=0&t2=1000")
	if err != nil {
		t.Fatalf("GET candidates: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got []candidateInfo
	err = json.NewDecoder(resp.Body).Decode(&got)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %+v, want exactly one (channel 1 only in fcontainer 1)", got)
	}
	wantUUID := hex.EncodeToString(uuid1[:])
	if got[0].UUID != wantUUID || got[0].Begin != 100 || got[0].End != 200 {
		t.Fatalf("candidate = %+v, want uuid=%s begin=100 end=200", got[0], wantUUID)
	}
}

func TestHandleCandidates_NoMatch(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/s1/candidates?channel=99&t1=0&t2=1000")
	if err != nil {
		t.Fatalf("GET candidates: %v", err)
	}
	defer resp.Body.Close()
	var got []candidateInfo
	err = json.NewDecoder(resp.Body).Decode(&got)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("candidates = %+v, want none", got)
	}
}

// TestHandleResolve writes the same channel (2) into two separate
// fcontainers and checks the fallback resolve (ADR-016) aggregates matching
// frames' actual data across both candidates, each confirmed by its own
// TOC (not just the fblock-level channel_bitmap hit).
func TestHandleResolve(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	writeVideoFrame(t, u, []uint16{2}, 2, 100, 200, "frame-a", 100, 1000)
	writeVideoFrame(t, u, []uint16{2}, 2, 300, 400, "frame-b", 300, 2000)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/s1/resolve?channel=2&t1=0&t2=1000")
	if err != nil {
		t.Fatalf("GET resolve: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var frames []resolvedFrame
	err = json.NewDecoder(resp.Body).Decode(&frames)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want 2", frames)
	}
	gotData := map[string]bool{}
	for _, f := range frames {
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			t.Fatalf("base64 decode: %v", err)
		}
		gotData[string(data)] = true
		if f.Kind == nil || *f.Kind != mediatree.FrameKindI {
			t.Fatalf("frame kind = %v, want I", f.Kind)
		}
	}
	if !gotData["frame-a"] || !gotData["frame-b"] {
		t.Fatalf("gotData = %+v, want frame-a and frame-b", gotData)
	}
}

func TestHandleResolve_ChannelNotPresent(t *testing.T) {
	reg := NewStorageRegistry()
	u := newTestUnit(t)
	writeVideoFrame(t, u, []uint16{1}, 1, 100, 200, "frame-a", 100, 1000)
	err := reg.Register("s1", u, "s1.img", "", storage.PoolTuning{})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := newTestServer(t, reg)

	resp, err := http.Get(srv.URL + "/storages/s1/resolve?channel=99&t1=0&t2=1000")
	if err != nil {
		t.Fatalf("GET resolve: %v", err)
	}
	defer resp.Body.Close()
	var frames []resolvedFrame
	err = json.NewDecoder(resp.Body).Decode(&frames)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frames = %+v, want none", frames)
	}
}
