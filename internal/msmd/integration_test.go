package msmd

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/traycers/farc/internal/api"
	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/msmapi"
	"github.com/traycers/farc/internal/msmclient"
)

// recordingHTTPServer is a minimal fake msm: it accepts every request with
// 200 and records method+path+decoded JSON body, in arrival order.
type recordingHTTPServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

type recordedRequest struct {
	method, path string
	body         map[string]any
}

func newRecordingHTTPServer(t *testing.T) *recordingHTTPServer {
	t.Helper()
	rs := &recordingHTTPServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.ContentLength != 0 {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		rs.mu.Lock()
		rs.requests = append(rs.requests, recordedRequest{method: r.Method, path: r.URL.RequestURI(), body: body})
		rs.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingHTTPServer) snapshot() []recordedRequest {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]recordedRequest, len(rs.requests))
	copy(out, rs.requests)
	return out
}

// TestIntegration_FullStack wires a real internal/api HTTP+WS server (backed
// by a real Storage), a real msmclient/hlsclient pair talking to it, and a
// real fake-msm HTTP server -- proving the whole real wire path works, not
// just each layer's own unit tests: a real fblock write triggers real
// storage.Event notifications, bridged into the global feed exactly as
// internal/farcd does, consumed over a real WS connection, decoded from a
// real TOC, and reported to the fake msm server in the order
// temp/msm/openapi.yaml requires (vaa_blocks_add before info_set for the
// same fblock).
func TestIntegration_FullStack(t *testing.T) {
	unit := newTestUnit(t)
	reg := api.NewStorageRegistry()
	err := reg.Register("arch1", unit, "/dev/null", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := api.NewEventPushServer(reg)
	apiSrv := api.NewHttpApiServer(reg, nil, push)
	httpTest := httptest.NewServer(apiSrv.Handler())
	defer httpTest.Close()
	wsBase := "ws" + strings.TrimPrefix(httpTest.URL, "http")

	msmSrv := newRecordingHTTPServer(t)

	// Bridge this Storage's fblock lifecycle into the global feed exactly as
	// internal/farcd.bridgeFblockEvents does in the real process.
	events := unit.Notify().Subscribe(64)
	defer unit.Notify().Unsubscribe(events)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				var name string
				switch ev.Name {
				case "fblock.write.started":
					name = api.EventFblockCreated
				case "fblock.write.completed":
					name = api.EventFblockReady
				case "fblock.deleted":
					name = api.EventFblockDeleted
				default:
					continue
				}
				je := api.JournalEvent{Name: name, Storage: "arch1", Index: ev.Index, UUID: hex.EncodeToString(ev.UUID[:])}
				if name == api.EventFblockReady {
					snap := unit.Index().Snapshot()
					if ev.Index < snap.N {
						je.Begin, je.End = snap.Begin[ev.Index], snap.End[ev.Index]
					}
				}
				push.Publish(je)
			}
		}
	}()

	out := msmapi.New(msmSrv.URL)
	content := hlsclient.New(httpTest.URL, "")
	p := newProcessor(out, content, t.Logf)
	sub := msmclient.New(wsBase)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msmEvents, err := sub.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	consumeDone := make(chan struct{})
	go func() {
		defer close(consumeDone)
		p.consume(ctx, msmEvents)
	}()

	// Give the WS subscription time to register before the write happens --
	// same reasoning as internal/api/eventpush_test.go's own sibling tests.
	time.Sleep(50 * time.Millisecond)

	writeChannelVideo(t, unit, 1, []uint64{0, second}, 0)

	deadline := time.After(3 * time.Second)
	for {
		reqs := msmSrv.snapshot()
		if len(reqs) >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for 3 requests to msm, got %d: %+v", len(reqs), reqs)
		case <-time.After(20 * time.Millisecond):
		}
	}

	reqs := msmSrv.snapshot()
	if len(reqs) < 3 {
		t.Fatalf("requests = %+v, want at least 3", reqs)
	}
	// fblock.created's fblocks_add arrives first (unit writes into a fresh
	// block), then params_add, then vaa_blocks_add, and info_set only after
	// -- assert the ordering constraint that actually matters: vaa_blocks_add
	// strictly before info_set.
	var vaaIdx, infoIdx = -1, -1
	for i, r := range reqs {
		if strings.HasSuffix(r.path, "/vaa_blocks/") && vaaIdx < 0 {
			vaaIdx = i
		}
		if strings.Contains(r.path, "/info/") && infoIdx < 0 {
			infoIdx = i
		}
	}
	if vaaIdx < 0 || infoIdx < 0 {
		t.Fatalf("requests = %+v, want both a vaa_blocks_add and an info_set call", reqs)
	}
	if vaaIdx >= infoIdx {
		t.Fatalf("vaa_blocks_add (request %d) did not precede info_set (request %d): %+v", vaaIdx, infoIdx, reqs)
	}

	cancel()
	<-consumeDone
}
