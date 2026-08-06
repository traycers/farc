package hlsapi_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"traycers/farc/fblock"
	"traycers/farc/internal/api"
	"traycers/farc/internal/fcontainer"
	"traycers/farc/internal/ioengine"
	"traycers/farc/internal/storage"
	"traycers/farc/mediatree"
)

// Recreated from internal/api/testutil_test.go (an unexported _test.go
// helper, not importable across packages) — see PLAN.md's Gap resolutions.

func smallGeometry() storage.Geometry {
	return storage.Geometry{FblockSize: 65536, N: 4, MaxChannels: 8}
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

// annexB wraps each nalu with a 4-byte start code, mirroring
// internal/ingest/rtsp.go's muxAnnexB — the format fcontainer's own video
// frame Data is always stored in.
func annexB(nalus ...[]byte) []byte {
	var out []byte
	for _, n := range nalus {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}

// A real, parseable 352x288 H.264 SPS (github.com/bluenviron/mediacommon/v2's
// own h264 test fixture) — fmp4.Init.Marshal actually decodes the SPS (for
// width/height), unlike PPS which is only length-checked.
var testSPS = []byte{
	0x67, 0x64, 0x00, 0x0c, 0xac, 0x3b, 0x50, 0xb0,
	0x4b, 0x42, 0x00, 0x00, 0x03, 0x00, 0x02, 0x00,
	0x00, 0x03, 0x00, 0x3d, 0x08,
}
var testPPS = []byte{0x68, 0xee, 0x3c, 0x80}

// A valid AAC-LC 44.1kHz mono AudioSpecificConfig (mpeg4audio's own test
// fixture).
var testASC = []byte{0x12, 0x08}

type videoFrameSpec struct {
	Time uint64
	Kind uint8
	NAL  []byte
}

type audioFrameSpec struct {
	Time uint64
	AU   []byte
}

// writeAVFcontainer writes one fcontainer with both a video and an audio
// stream for channel, returning its UUID.
func writeAVFcontainer(t *testing.T, unit *storage.Unit, channel uint32, videoFrames []videoFrameSpec, audioFrames []audioFrameSpec, begin, end, now uint64) [16]byte {
	t.Helper()
	f := fcontainer.New()

	videoConfigID, err := f.AddStreamParams(channel, 0, fcontainer.KindVideo, fcontainer.StreamParams{
		Time:       videoFrames[0].Time,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   testSPS,
		ParamPPS:   testPPS,
	})
	if err != nil {
		t.Fatalf("AddStreamParams(video): %v", err)
	}
	vFrames := make([]fcontainer.Frame, len(videoFrames))
	for i, vf := range videoFrames {
		vFrames[i] = fcontainer.Frame{Data: annexB(vf.NAL), Time: vf.Time, Kind: vf.Kind}
	}
	err = f.AddFrames(videoConfigID, vFrames)
	if err != nil {
		t.Fatalf("AddFrames(video): %v", err)
	}

	if len(audioFrames) > 0 {
		audioConfigID, err := f.AddStreamParams(channel, 0, fcontainer.KindAudio, fcontainer.StreamParams{
			Time:             audioFrames[0].Time,
			CodecAudio:       mediatree.CodecAAC,
			SampleRate:       44100,
			ChannelCount:     1,
			ParamAudioConfig: testASC,
		})
		if err != nil {
			t.Fatalf("AddStreamParams(audio): %v", err)
		}
		aFrames := make([]fcontainer.Frame, len(audioFrames))
		for i, af := range audioFrames {
			aFrames[i] = fcontainer.Frame{Data: af.AU, Time: af.Time}
		}
		err = f.AddFrames(audioConfigID, aFrames)
		if err != nil {
			t.Fatalf("AddFrames(audio): %v", err)
		}
	}

	uuid, err := unit.WriteFcontainer([]uint16{uint16(channel)}, begin, end, f, now)
	if err != nil {
		t.Fatalf("WriteFcontainer: %v", err)
	}
	return uuid
}

type farcdTestServer struct {
	*httptest.Server
	wsURL string
}

func newFarcdTestServer(t *testing.T, unit *storage.Unit) *farcdTestServer {
	t.Helper()
	reg := api.NewStorageRegistry()
	err := reg.Register("s1", unit, "/dev/null", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	push := api.NewEventPushServer(reg)
	srv := api.NewHttpApiServer(reg, nil, push)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &farcdTestServer{Server: ts, wsURL: "ws" + strings.TrimPrefix(ts.URL, "http")}
}
