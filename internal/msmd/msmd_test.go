package msmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"traycers/farc/fblock"
	"traycers/farc/internal/fcontainer"
	"traycers/farc/internal/hlsclient"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/msmapi"
	"traycers/farc/internal/msmclient"
	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
	"traycers/farc/toc"
)

// call/fakeOutbound records every outbound method invocation, in order, for
// assertions on both which calls happened and their relative sequence
// (vaa_blocks_add before info_set, in particular).
type call struct {
	method string
	args   []any
}

type fakeOutbound struct {
	calls []call
	err   error
}

func (f *fakeOutbound) record(method string, args ...any) error {
	f.calls = append(f.calls, call{method, args})
	return f.err
}

func (f *fakeOutbound) ParamsAdd(_ context.Context, aid string, id int64, streamType int, data any) error {
	return f.record("ParamsAdd", aid, id, streamType, data)
}

func (f *fakeOutbound) FblocksAdd(_ context.Context, aid string, id [16]byte, num int64) error {
	return f.record("FblocksAdd", aid, id, num)
}

func (f *fakeOutbound) FblocksDel(_ context.Context, aid string, id [16]byte) error {
	return f.record("FblocksDel", aid, id)
}

func (f *fakeOutbound) InfoSet(_ context.Context, aid string, fbid [16]byte, num int64, status int, start, stop time.Time) error {
	return f.record("InfoSet", aid, fbid, num, status, start, stop)
}

func (f *fakeOutbound) StartedAdd(_ context.Context, aid string, channel uint16, begin time.Time) error {
	return f.record("StartedAdd", aid, channel, begin)
}

func (f *fakeOutbound) FinishedAdd(_ context.Context, aid string, channel uint16, end time.Time) error {
	return f.record("FinishedAdd", aid, channel, end)
}

func (f *fakeOutbound) VaaBlocksAdd(_ context.Context, aid string, block msmapi.VaaBlock) error {
	return f.record("VaaBlocksAdd", aid, block)
}

func (f *fakeOutbound) methods() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.method
	}
	return out
}

// unitContentFetcher adapts a real storage.Unit to the contentFetcher
// interface, so tests exercise resolveConfig's actual range-batching logic
// against real fblock content rather than canned bytes.
type unitContentFetcher struct{ unit *storage.Unit }

func (u unitContentFetcher) ReadRanges(_ context.Context, _ string, uuid [16]byte, ranges []hlsclient.Range) ([][]byte, error) {
	storageRanges := make([]storage.Range, len(ranges))
	for i, r := range ranges {
		storageRanges[i] = storage.Range{Offset: r.Offset, Size: r.Size}
	}
	return u.unit.ReadRanges(uuid, storageRanges)
}

// Recreated from internal/api/testutil_test.go, same as every other
// package's own copy (internal/hlsclient, internal/msmclient,
// internal/vaablocks) -- not importable across packages.

func smallGeometry() storage.Geometry {
	return storage.Geometry{FblockSize: 65536, N: 4, MaxChannels: 8}
}

func smallParams() fblock.Params {
	return fblock.Params{
		FchunkSize:        4096,
		ReadChunkSize:     512,
		WriteMode:         fblock.WriteModeCyclic,
		Retention:         fblock.Retention{Days: 30},
		MinContainerShare: fblock.DefaultMinContainerShare,
	}
}

func newTestUnit(t *testing.T) *storage.Unit {
	t.Helper()
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "storage.img")
	geo := smallGeometry()

	err := storage.CreateSizedFile(imgPath, int64(geo.FblockSize)*int64(geo.N), 0o644)
	if err != nil {
		t.Fatalf("CreateSizedFile: %v", err)
	}
	b, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenStandard: %v", err)
	}
	err = storage.Init(b, storage.InitConfig{Geometry: geo, Params: smallParams(), Now: 1})
	if err != nil {
		b.Close()
		t.Fatalf("Init: %v", err)
	}
	err = b.Close()
	if err != nil {
		t.Fatalf("close after init: %v", err)
	}

	b2, err := ioengine.OpenStandard(imgPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	u, err := storage.Open(storage.OpenConfig{Backend: b2})
	if err != nil {
		b2.Close()
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { u.Close() })
	return u
}

const second = uint64(1_000_000_000)

// writeChannelVideo writes one channel's video frames at the given times
// (with a fixed SPS/PPS pair), returning the fblock's uuid and its raw TOC
// bytes (the shape a real "toc" WS frame carries).
func writeChannelVideo(t *testing.T, unit *storage.Unit, channel uint16, times []uint64, configTime uint64) ([16]byte, []byte) {
	t.Helper()
	f := fcontainer.New()
	cid, err := f.AddStreamParams(uint32(channel), 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time: configTime, CodecVideo: mediatree.CodecH264, ParamSPS: []byte{1, 2, 3}, ParamPPS: []byte{4, 5},
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	frames := make([]fcontainer.Frame, len(times))
	for i, tm := range times {
		frames[i] = fcontainer.Frame{Data: []byte(fmt.Sprintf("frame-%d-payload", i)), Time: tm, Kind: mediatree.FrameKindI}
	}
	err = f.AddFrames(cid, frames)
	if err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	uuid, err := unit.WriteFcontainer([]uint16{channel}, times[0], times[len(times)-1], f, 1000)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}
	buf, err := toc.Encode(columns)
	if err != nil {
		t.Fatalf("toc.Encode: %v", err)
	}
	return uuid, buf
}

func TestHandle_FblockCreated(t *testing.T) {
	out := &fakeOutbound{}
	p := newProcessor(out, nil, nil)
	var uuid [16]byte
	uuid[0] = 0xab
	err := p.handle(context.Background(), msmclient.Event{Type: "event", Name: eventFblockCreated, Storage: "arch1", Index: 3, UUID: uuid, HasUUID: true}, nil)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(out.calls) != 1 || out.calls[0].method != "FblocksAdd" {
		t.Fatalf("calls = %+v", out.calls)
	}
	if out.calls[0].args[0] != "arch1" || out.calls[0].args[1] != uuid || out.calls[0].args[2] != int64(3) {
		t.Fatalf("FblocksAdd args = %+v", out.calls[0].args)
	}
}

func TestHandle_FblockCreated_MissingUUID(t *testing.T) {
	out := &fakeOutbound{}
	p := newProcessor(out, nil, nil)
	err := p.handle(context.Background(), msmclient.Event{Type: "event", Name: eventFblockCreated, Storage: "arch1"}, nil)
	if err == nil {
		t.Fatal("expected an error for a missing uuid")
	}
	if len(out.calls) != 0 {
		t.Fatalf("calls = %+v, want none", out.calls)
	}
}

func TestHandle_FblockDeleted(t *testing.T) {
	out := &fakeOutbound{}
	p := newProcessor(out, nil, nil)
	var uuid [16]byte
	err := p.handle(context.Background(), msmclient.Event{Type: "event", Name: eventFblockDeleted, Storage: "arch1", UUID: uuid, HasUUID: true}, nil)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(out.calls) != 1 || out.calls[0].method != "FblocksDel" {
		t.Fatalf("calls = %+v", out.calls)
	}
}

func TestHandle_RecordingStartedAndStopped(t *testing.T) {
	out := &fakeOutbound{}
	p := newProcessor(out, nil, nil)

	err := p.handle(context.Background(), msmclient.Event{Type: "event", Name: eventRecordingStarted, Storage: "arch1", Channel: 5, Begin: 1000}, nil)
	if err != nil {
		t.Fatalf("handle started: %v", err)
	}
	err = p.handle(context.Background(), msmclient.Event{Type: "event", Name: eventRecordingStopped, Storage: "arch1", Channel: 5, End: 2000}, nil)
	if err != nil {
		t.Fatalf("handle stopped: %v", err)
	}
	if got := out.methods(); len(got) != 2 || got[0] != "StartedAdd" || got[1] != "FinishedAdd" {
		t.Fatalf("methods = %+v", got)
	}
}

func TestHandle_RecordingStarted_MissingStorage(t *testing.T) {
	out := &fakeOutbound{}
	p := newProcessor(out, nil, nil)
	err := p.handle(context.Background(), msmclient.Event{Type: "event", Name: eventRecordingStarted, Channel: 5, Begin: 1000}, nil)
	if err == nil {
		t.Fatal("expected an error for a missing storage/archive id")
	}
}

func TestHandleFblockReady_FullFlow(t *testing.T) {
	unit := newTestUnit(t)
	uuid, tocBytes := writeChannelVideo(t, unit, 1, []uint64{0, second}, 0)

	out := &fakeOutbound{}
	p := newProcessor(out, unitContentFetcher{unit}, nil)

	events := make(chan msmclient.Event, 2)
	events <- msmclient.Event{Type: "event", Name: eventFblockReady, Storage: "arch1", Index: 7, UUID: uuid, HasUUID: true, Begin: 0, End: second}
	events <- msmclient.Event{Type: "toc", Storage: "arch1", Index: 7, UUID: uuid, TOC: tocBytes}

	ev := <-events
	err := p.handle(context.Background(), ev, events)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	methods := out.methods()
	if len(methods) != 3 || methods[0] != "ParamsAdd" || methods[1] != "VaaBlocksAdd" || methods[2] != "InfoSet" {
		t.Fatalf("methods = %+v, want [ParamsAdd VaaBlocksAdd InfoSet]", methods)
	}

	paramsCall := out.calls[0]
	if paramsCall.args[0] != "arch1" || paramsCall.args[1] != int64(1) || paramsCall.args[2] != streamTypeVideo {
		t.Fatalf("ParamsAdd args = %+v", paramsCall.args)
	}
	data, ok := paramsCall.args[3].(map[string]any)
	if !ok || data["codec"] != "h264" || data["sps"] == nil || data["pps"] == nil {
		t.Fatalf("ParamsAdd data = %+v", paramsCall.args[3])
	}

	vaaCall := out.calls[1]
	block, ok := vaaCall.args[1].(msmapi.VaaBlock)
	if !ok || block.ParamsID != 1 || block.ChannelNum != 1 || block.StreamType != streamTypeVideo || block.FblockID != uuid || block.ID.Fnum != 7 {
		t.Fatalf("VaaBlocksAdd block = %+v", block)
	}

	infoCall := out.calls[2]
	if infoCall.args[0] != "arch1" || infoCall.args[1] != uuid || infoCall.args[2] != int64(7) || infoCall.args[3] != int(fblock.Ready) {
		t.Fatalf("InfoSet args = %+v", infoCall.args)
	}
}

func TestHandleFblockReady_ParamsDedupAcrossFblocks(t *testing.T) {
	unit := newTestUnit(t)
	// Same configTime (0) both times -- same real params, per two different
	// recording segments -- must reuse one params_id, not mint a second.
	uuid1, toc1 := writeChannelVideo(t, unit, 1, []uint64{0, second}, 0)
	uuid2, toc2 := writeChannelVideo(t, unit, 1, []uint64{10 * second, 11 * second}, 0)

	out := &fakeOutbound{}
	p := newProcessor(out, unitContentFetcher{unit}, nil)

	for _, step := range []struct {
		uuid [16]byte
		toc  []byte
		idx  uint32
	}{{uuid1, toc1, 1}, {uuid2, toc2, 2}} {
		events := make(chan msmclient.Event, 2)
		events <- msmclient.Event{Type: "event", Name: eventFblockReady, Storage: "arch1", Index: step.idx, UUID: step.uuid, HasUUID: true}
		events <- msmclient.Event{Type: "toc", Storage: "arch1", Index: step.idx, UUID: step.uuid, TOC: step.toc}
		ev := <-events
		err := p.handle(context.Background(), ev, events)
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
	}

	paramsAddCount := 0
	for _, m := range out.methods() {
		if m == "ParamsAdd" {
			paramsAddCount++
		}
	}
	if paramsAddCount != 1 {
		t.Fatalf("ParamsAdd called %d times, want 1 (params unchanged across fblocks)", paramsAddCount)
	}
}

// fakeSubscriber's Subscribe returns queued (events channel, error) pairs in
// order, one per call, recording how many times it was invoked.
type fakeSubscriber struct {
	calls int
	steps []func() (<-chan msmclient.Event, error)
}

func (f *fakeSubscriber) Subscribe(context.Context) (<-chan msmclient.Event, error) {
	i := f.calls
	f.calls++
	if i >= len(f.steps) {
		i = len(f.steps) - 1
	}
	return f.steps[i]()
}

func closedChan() (<-chan msmclient.Event, error) {
	ch := make(chan msmclient.Event)
	close(ch)
	return ch, nil
}

func TestRun_ReconnectsOnDisconnect(t *testing.T) {
	sub := &fakeSubscriber{steps: []func() (<-chan msmclient.Event, error){closedChan}}
	p := newProcessor(&fakeOutbound{}, nil, func(string, ...any) {})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	run(ctx, sub, p, func(string, ...any) {})

	if sub.calls < 2 {
		t.Fatalf("Subscribe called %d times, want at least 2 (should reconnect on disconnect)", sub.calls)
	}
}

func TestRun_ReconnectsAfterSubscribeError(t *testing.T) {
	errOnce := true
	sub := &fakeSubscriber{steps: []func() (<-chan msmclient.Event, error){
		func() (<-chan msmclient.Event, error) {
			if errOnce {
				errOnce = false
				return nil, errors.New("dial failed")
			}
			return closedChan()
		},
	}}
	p := newProcessor(&fakeOutbound{}, nil, func(string, ...any) {})

	ctx, cancel := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer cancel()
	run(ctx, sub, p, func(string, ...any) {})

	if sub.calls < 2 {
		t.Fatalf("Subscribe called %d times, want at least 2 (should retry after an error)", sub.calls)
	}
}
