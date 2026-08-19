package segment_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"

	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/segment"
	"github.com/traycers/farc/internal/tocindex"
	"github.com/traycers/farc/mediatree"
)

func setupFixture(t *testing.T) (*hlsclient.Client, tocindex.Record) {
	t.Helper()
	unit := newTestUnit(t)

	videoFrames := []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xaa, 0xbb, 0xcc}},
		{Time: 1_000_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41, 0xdd}},
		{Time: 2_000_000, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xee, 0xff}},
	}
	audioFrames := []audioFrameSpec{
		{Time: 0, AU: []byte{0x01, 0x02, 0x03}},
		{Time: 1_000_000, AU: []byte{0x04, 0x05}},
		{Time: 2_000_000, AU: []byte{0x06}},
	}
	uuid := writeAVFcontainer(t, unit, 1, videoFrames, audioFrames, 0, 2_100_000, 1000)

	ts := newTestServer(t, unit)
	client := hlsclient.New(ts.URL, ts.wsURL)

	columns, err := client.GetTOC(context.Background(), "s1", uuid)
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	rec := tocindex.Record{UUID: uuid, StorageID: "s1", Begin: 0, End: 2_100_000, Columns: columns}
	return client, rec
}

func TestBuildInit_RoundTrips(t *testing.T) {
	client, rec := setupFixture(t)
	ctx := context.Background()

	initBytes, err := segment.BuildInit(ctx, client, rec, 1)
	if err != nil {
		t.Fatalf("BuildInit: %v", err)
	}

	var parsed fmp4.Init
	err = parsed.Unmarshal(bytes.NewReader(initBytes))
	if err != nil {
		t.Fatalf("fmp4.Init.Unmarshal: %v", err)
	}
	if len(parsed.Tracks) != 2 {
		t.Fatalf("Tracks = %d, want 2", len(parsed.Tracks))
	}

	var video *fmp4.InitTrack
	var audio *fmp4.InitTrack
	for _, tr := range parsed.Tracks {
		switch tr.Codec.(type) {
		case *fmp4.CodecH264:
			video = tr
		case *fmp4.CodecMPEG4Audio:
			audio = tr
		}
	}
	if video == nil || audio == nil {
		t.Fatalf("Tracks = %+v, want one H264 and one MPEG4Audio track", parsed.Tracks)
	}
	if video.TimeScale != 1_000_000_000 || audio.TimeScale != 1_000_000_000 {
		t.Fatalf("TimeScale video=%d audio=%d, want 1e9 for both", video.TimeScale, audio.TimeScale)
	}

	h264Codec := video.Codec.(*fmp4.CodecH264)
	if !bytes.Equal(h264Codec.SPS, testSPS) || !bytes.Equal(h264Codec.PPS, testPPS) {
		t.Fatalf("SPS/PPS = %x/%x, want %x/%x", h264Codec.SPS, h264Codec.PPS, testSPS, testPPS)
	}

	aacCodec := audio.Codec.(*fmp4.CodecMPEG4Audio)
	if aacCodec.SampleRate != 44100 || aacCodec.ChannelCount != 1 {
		t.Fatalf("AAC config = %+v, want SampleRate=44100 ChannelCount=1", aacCodec.Config)
	}
}

func TestBuildMedia_FullWindow_RoundTrips(t *testing.T) {
	client, rec := setupFixture(t)
	ctx := context.Background()

	mediaBytes, err := segment.BuildMedia(ctx, client, rec, 1, 0, 0, 2_100_000)
	if err != nil {
		t.Fatalf("BuildMedia: %v", err)
	}

	var parts fmp4.Parts
	err = parts.Unmarshal(mediaBytes)
	if err != nil {
		t.Fatalf("fmp4.Parts.Unmarshal: %v", err)
	}
	if len(parts) != 1 || len(parts[0].Tracks) != 2 {
		t.Fatalf("parts = %+v, want 1 part with 2 tracks", parts)
	}

	var video, audio *fmp4.PartTrack
	for _, tr := range parts[0].Tracks {
		switch tr.ID {
		case 1:
			video = tr
		case 2:
			audio = tr
		}
	}
	if video == nil || audio == nil {
		t.Fatalf("tracks = %+v, want track IDs 1 (video) and 2 (audio)", parts[0].Tracks)
	}

	if len(video.Samples) != 3 {
		t.Fatalf("video samples = %d, want 3", len(video.Samples))
	}
	wantDur := []uint32{1_000_000, 1_000_000, 100_000}
	wantSync := []bool{true, false, true}
	wantNAL := [][]byte{{0x65, 0xaa, 0xbb, 0xcc}, {0x41, 0xdd}, {0x65, 0xee, 0xff}}
	for i, s := range video.Samples {
		if s.Duration != wantDur[i] {
			t.Fatalf("video sample %d Duration = %d, want %d", i, s.Duration, wantDur[i])
		}
		if s.IsNonSyncSample == wantSync[i] {
			t.Fatalf("video sample %d IsNonSyncSample = %v, want sync=%v", i, s.IsNonSyncSample, wantSync[i])
		}
		nalus, err := s.GetH264()
		if err != nil {
			t.Fatalf("video sample %d GetH264: %v", i, err)
		}
		if len(nalus) != 1 || !bytes.Equal(nalus[0], wantNAL[i]) {
			t.Fatalf("video sample %d NALs = %x, want [%x]", i, nalus, wantNAL[i])
		}
	}

	if len(audio.Samples) != 3 {
		t.Fatalf("audio samples = %d, want 3", len(audio.Samples))
	}
	wantAU := [][]byte{{0x01, 0x02, 0x03}, {0x04, 0x05}, {0x06}}
	for i, s := range audio.Samples {
		if s.Duration != wantDur[i] {
			t.Fatalf("audio sample %d Duration = %d, want %d", i, s.Duration, wantDur[i])
		}
		if !bytes.Equal(s.Payload, wantAU[i]) {
			t.Fatalf("audio sample %d Payload = %x, want %x", i, s.Payload, wantAU[i])
		}
	}
}

func TestBuildMedia_PartitionsAcrossTwoSegmentsWithNoOverlap(t *testing.T) {
	client, rec := setupFixture(t)
	ctx := context.Background()

	firstBytes, err := segment.BuildMedia(ctx, client, rec, 1, 0, 0, 1_000_000)
	if err != nil {
		t.Fatalf("BuildMedia (first): %v", err)
	}
	var firstParts fmp4.Parts
	err = firstParts.Unmarshal(firstBytes)
	if err != nil {
		t.Fatalf("Unmarshal (first): %v", err)
	}
	firstVideo := trackByID(t, firstParts[0].Tracks, 1)
	if len(firstVideo.Samples) != 1 {
		t.Fatalf("first segment video samples = %d, want exactly 1 (the frame at t=1e6 belongs to the next segment)", len(firstVideo.Samples))
	}
	if firstVideo.Samples[0].Duration != 1_000_000 {
		t.Fatalf("first segment sample Duration = %d, want 1_000_000", firstVideo.Samples[0].Duration)
	}
	// BaseTime must be the segment's own first sample's absolute time (ns),
	// not 0 -- otherwise every segment's tfdt claims to start the track's
	// timeline over from zero, which is what was silently producing
	// mediaError/bufferAppendError in real browsers (.scratch/hls-playback/
	// issues/01-media-segment-basetime-sequencenumber.md): MSE positions
	// each fragment's samples using tfdt, so two fragments both claiming
	// baseMediaDecodeTime=0 overlap entirely on the SourceBuffer timeline.
	firstAudio := trackByID(t, firstParts[0].Tracks, 2)
	if firstVideo.BaseTime != 0 || firstAudio.BaseTime != 0 {
		t.Fatalf("first segment BaseTime video=%d audio=%d, want 0 (first frame of the record)", firstVideo.BaseTime, firstAudio.BaseTime)
	}

	secondBytes, err := segment.BuildMedia(ctx, client, rec, 1, 1, 1_000_000, 2_100_000)
	if err != nil {
		t.Fatalf("BuildMedia (second): %v", err)
	}
	var secondParts fmp4.Parts
	err = secondParts.Unmarshal(secondBytes)
	if err != nil {
		t.Fatalf("Unmarshal (second): %v", err)
	}
	secondVideo := trackByID(t, secondParts[0].Tracks, 1)
	if len(secondVideo.Samples) != 2 {
		t.Fatalf("second segment video samples = %d, want exactly 2 (frames at t=1e6 and t=2e6)", len(secondVideo.Samples))
	}
	if secondVideo.Samples[0].Duration != 1_000_000 || secondVideo.Samples[1].Duration != 100_000 {
		t.Fatalf("second segment durations = [%d,%d], want [1000000,100000]", secondVideo.Samples[0].Duration, secondVideo.Samples[1].Duration)
	}
	secondAudio := trackByID(t, secondParts[0].Tracks, 2)
	if secondVideo.BaseTime != 1_000_000 || secondAudio.BaseTime != 1_000_000 {
		t.Fatalf("second segment BaseTime video=%d audio=%d, want 1000000 (this segment's first frame)", secondVideo.BaseTime, secondAudio.BaseTime)
	}

	// SequenceNumber must be unique/increasing across a record's fragments
	// -- browsers' native fMP4 demuxers (the ones actually processing these
	// bytes, since hls.js passes already-fragmented CMAF through untouched)
	// reject a repeated mfhd sequence_number.
	if firstParts[0].SequenceNumber == secondParts[0].SequenceNumber {
		t.Fatalf("SequenceNumber first=%d second=%d, want different values", firstParts[0].SequenceNumber, secondParts[0].SequenceNumber)
	}
}

func trackByID(t *testing.T, tracks []*fmp4.PartTrack, id int) *fmp4.PartTrack {
	t.Helper()
	for _, tr := range tracks {
		if tr.ID == id {
			return tr
		}
	}
	t.Fatalf("no track with ID %d among %+v", id, tracks)
	return nil
}
