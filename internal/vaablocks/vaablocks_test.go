package vaablocks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"traycers/farc/fblock"
	"traycers/farc/internal/fcontainer"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
	"traycers/farc/internal/vaablocks"
	"traycers/farc/mediatree"
)

// Recreated from internal/api/testutil_test.go, same as internal/hlsclient's
// and internal/msmclient's own copies (an unexported _test.go helper, not
// importable across packages).

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

// writeChannelVideo writes one channel's video frames (only) at the given
// times into a fresh fcontainer/fblock, returning its UUID.
func writeChannelVideo(t *testing.T, unit *storage.Unit, channel uint16, times []uint64) [16]byte {
	t.Helper()
	f := fcontainer.New()
	cid, err := f.AddStreamParams(uint32(channel), 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time: times[0], CodecVideo: mediatree.CodecH264, ParamSPS: []byte{1, 2, 3}, ParamPPS: []byte{4, 5},
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
	return uuid
}

const second = uint64(1_000_000_000)

func TestCompute_SplitsOnGap(t *testing.T) {
	unit := newTestUnit(t)
	// gap between times[2] (2s) and times[3] (5s) is 3s >= GapThresholdNS (2s):
	// two vaa-blocks, {0,1s,2s} and {5s,6s}.
	times := []uint64{0, second, 2 * second, 5 * second, 6 * second}
	uuid := writeChannelVideo(t, unit, 1, times)
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}

	blocks, err := vaablocks.Compute(columns, 1)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v, want 2", blocks)
	}
	if blocks[0].Channel != 1 || blocks[0].Begin != times[0] || blocks[0].End != times[2] || blocks[0].StreamID != 0 {
		t.Fatalf("blocks[0] = %+v", blocks[0])
	}
	if blocks[1].Channel != 1 || blocks[1].Begin != times[3] || blocks[1].End != times[4] || blocks[1].StreamID != 0 {
		t.Fatalf("blocks[1] = %+v", blocks[1])
	}
	if blocks[0].ConfigID != blocks[1].ConfigID {
		t.Fatalf("blocks = %+v, want same ConfigID (params never changed)", blocks)
	}
	// Each block's byte span must be non-empty and the second must start
	// strictly after the first ends (frames are written in order).
	if blocks[0].Size == 0 || blocks[1].Size == 0 {
		t.Fatalf("blocks = %+v, want non-zero sizes", blocks)
	}
	if blocks[1].Offset < blocks[0].Offset+blocks[0].Size {
		t.Fatalf("blocks[1].Offset = %d, want >= end of blocks[0] (%d)", blocks[1].Offset, blocks[0].Offset+blocks[0].Size)
	}
}

func TestCompute_NoGapIsOneBlock(t *testing.T) {
	unit := newTestUnit(t)
	times := []uint64{0, second, 2 * second}
	uuid := writeChannelVideo(t, unit, 1, times)
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}

	blocks, err := vaablocks.Compute(columns, 1)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Begin != times[0] || blocks[0].End != times[2] {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestCompute_UnknownChannelReturnsNil(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeChannelVideo(t, unit, 1, []uint64{0, second})
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}

	blocks, err := vaablocks.Compute(columns, 99)
	if err != nil || blocks != nil {
		t.Fatalf("blocks = %+v, err = %v, want nil, nil", blocks, err)
	}
}

func TestStreamConfigs(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeChannelVideo(t, unit, 1, []uint64{0, second})
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}

	configs, err := vaablocks.StreamConfigs(columns, 1)
	if err != nil {
		t.Fatalf("StreamConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("configs = %+v, want 1", configs)
	}
	sc := configs[0]
	if sc.Kind != vaablocks.KindVideo || sc.StreamID != 0 {
		t.Fatalf("config = %+v", sc)
	}
	if sc.Codec != mediatree.CodecH264 {
		t.Fatalf("Codec = %d, want CodecH264", sc.Codec)
	}
	if !sc.HasSPS || sc.SPS.Size != 3 {
		t.Fatalf("SPS = %+v, want size 3 ([1,2,3])", sc.SPS)
	}
	if !sc.HasPPS || sc.PPS.Size != 2 {
		t.Fatalf("PPS = %+v, want size 2 ([4,5])", sc.PPS)
	}
	if sc.HasVPS {
		t.Fatalf("HasVPS = true, want false (writeChannelVideo never sets VPS)")
	}

	blocks, err := vaablocks.Compute(columns, 1)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(blocks) != 1 || blocks[0].ConfigID != sc.ConfigID {
		t.Fatalf("blocks = %+v, sc.ConfigID = %d", blocks, sc.ConfigID)
	}
}

func TestStreamConfigs_UnknownChannelReturnsNil(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeChannelVideo(t, unit, 1, []uint64{0, second})
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}

	configs, err := vaablocks.StreamConfigs(columns, 99)
	if err != nil || configs != nil {
		t.Fatalf("configs = %+v, err = %v, want nil, nil", configs, err)
	}
}

func TestChannels(t *testing.T) {
	unit := newTestUnit(t)
	uuid := writeChannelVideo(t, unit, 7, []uint64{0, second})
	columns, err := unit.ReadTOC(uuid)
	if err != nil {
		t.Fatalf("ReadTOC: %v", err)
	}

	channels := vaablocks.Channels(columns)
	if len(channels) != 1 || channels[0] != 7 {
		t.Fatalf("channels = %+v, want [7]", channels)
	}
}
