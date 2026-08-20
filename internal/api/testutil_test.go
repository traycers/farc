package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/traycers/farc/fblock"
	"github.com/traycers/farc/internal/fcontainer"
	"github.com/traycers/farc/internal/ioengine"
	"github.com/traycers/farc/internal/storage"
	"github.com/traycers/farc/mediatree"
)

func smallGeometry() storage.Geometry {
	return storage.Geometry{FblockSize: 8192, N: 4, MaxChannels: 8}
}

func smallParams() fblock.Params {
	return fblock.Params{
		FchunkSize:        1024,
		ReadChunkSize:     512,
		WriteMode:         fblock.WriteModeCyclic,
		Retention:         fblock.Retention{Days: 30},
		MinContainerShare: fblock.DefaultMinContainerShare,
	}
}

// newTestUnit initializes and opens a fresh, small standard-backend Storage
// image under t.TempDir(), returning the live Unit (closed automatically via
// t.Cleanup).
func newTestUnit(t *testing.T) *storage.Unit {
	t.Helper()
	return newTestUnitWithGeometry(t, smallGeometry())
}

// newTestUnitWithGeometry is newTestUnit with a caller-chosen Geometry --
// e.g. a small MaxChannels for capacity-limit tests (smallGeometry's
// MaxChannels: 8 is too high to hit in a handful of requests).
func newTestUnitWithGeometry(t *testing.T, geo storage.Geometry) *storage.Unit {
	t.Helper()
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "storage.img")

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

// writeVideoFrame builds a one-frame video fcontainer for channel and writes
// it via unit.WriteFcontainer, returning the resulting UUID.
func writeVideoFrame(t *testing.T, unit *storage.Unit, channels []uint16, channel uint32, begin, end uint64, frameData string, frameTime uint64, now uint64) [16]byte {
	t.Helper()
	f := fcontainer.New()
	configID, err := f.AddStreamParams(channel, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       frameTime,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   []byte{1, 2, 3},
		ParamPPS:   []byte{4, 5},
	})
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	if err := f.AddFrames(configID, []fcontainer.Frame{
		{Data: []byte(frameData), Time: frameTime, Kind: mediatree.FrameKindI},
	}); err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	uuid, err := unit.WriteFcontainer(channels, begin, end, f, now)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}
	return uuid
}
