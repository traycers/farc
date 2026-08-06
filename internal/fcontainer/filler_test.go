package fcontainer

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"

	"traycers/farc/mediatree"
)

func videoParams() StreamParams {
	return StreamParams{
		Time:       1000,
		CodecVideo: mediatree.CodecH264,
		ParamSPS:   []byte{1, 2, 3},
		ParamPPS:   []byte{4, 5},
	}
}

func audioParams() StreamParams {
	return StreamParams{
		Time:         2000,
		CodecAudio:   mediatree.CodecAAC,
		SampleRate:   48000,
		ChannelCount: 1,
	}
}

func TestAddStreamParamsAndFramesVideo(t *testing.T) {
	f := New()
	configID, err := f.AddStreamParams(1, 0, KindVideo, videoParams())
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	err = f.AddFrames(configID, []Frame{
		{Data: []byte("keyframe"), Time: 100, Kind: mediatree.FrameKindI},
		{Data: []byte("pframe1"), Time: 133, Kind: mediatree.FrameKindP},
		{Data: []byte("pframe2"), Time: 166, Kind: mediatree.FrameKindP},
	})
	if err != nil {
		t.Fatalf("AddFrames: %v", err)
	}

	elems := f.Elements()
	err = mediatree.Validate(elems)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// channel(1) must exist with value 1.
	chID, ok := mediatree.FindChildByRole(elems, 0, mediatree.RoleChannels)
	if !ok {
		t.Fatal("no channels container")
	}
	channelID, ok := mediatree.FindChildByRole(elems, chID, mediatree.RoleChannel)
	if !ok || binary.LittleEndian.Uint32(elems[channelID].Value) != 1 {
		t.Fatalf("channel node missing or wrong value")
	}

	// Three frame nodes under the frames container, in order.
	streamsID, _ := mediatree.FindChildByRole(elems, channelID, mediatree.RoleStreams)
	streamID, _ := mediatree.FindChildByRole(elems, streamsID, mediatree.RoleStream)
	videoID, _ := mediatree.FindChildByRole(elems, streamID, mediatree.RoleVideo)
	configsID, ok := mediatree.FindChildByRole(elems, videoID, mediatree.RoleConfigsVideo)
	if !ok {
		t.Fatal("no configs container under video")
	}
	if elems[configID].Parent != configsID {
		t.Fatalf("config's parent = %d, want configs container %d", elems[configID].Parent, configsID)
	}
	framesID, ok := mediatree.FindChildByRole(elems, configID, mediatree.RoleFramesVideo)
	if !ok {
		t.Fatal("no frames container under config")
	}
	frameIDs := mediatree.Children(elems, framesID)
	if len(frameIDs) != 3 {
		t.Fatalf("got %d frame nodes, want 3", len(frameIDs))
	}
	// Order preserved + GOP walk-back finds the keyframe from the last frame.
	kf, err := mediatree.FindKeyframe(elems, frameIDs[2])
	if err != nil {
		t.Fatalf("FindKeyframe: %v", err)
	}
	if kf != frameIDs[0] {
		t.Fatalf("FindKeyframe from last frame = %d, want first frame %d", kf, frameIDs[0])
	}
}

func TestAddStreamParamsAndFramesAudio(t *testing.T) {
	f := New()
	configID, err := f.AddStreamParams(5, 2, KindAudio, audioParams())
	if err != nil {
		t.Fatalf("AddStreamParams: %v", err)
	}
	err = f.AddFrames(configID, []Frame{{Data: []byte("pcm1"), Time: 10}, {Data: []byte("pcm2"), Time: 20}})
	if err != nil {
		t.Fatalf("AddFrames: %v", err)
	}
	elems := f.Elements()
	err = mediatree.Validate(elems)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	framesID, ok := mediatree.FindChildByRole(elems, configID, mediatree.RoleFramesAudio)
	if !ok {
		t.Fatal("no audio frames container")
	}
	frameIDs := mediatree.Children(elems, framesID)
	if len(frameIDs) != 2 {
		t.Fatalf("got %d frames, want 2", len(frameIDs))
	}
	// Audio frames must not have a frame_kind child.
	if _, ok := mediatree.FindChildByRole(elems, frameIDs[0], mediatree.RoleFrameKind); ok {
		t.Error("audio frame should not have a frame_kind child")
	}
}

func TestAddFramesWithUnknownConfigID(t *testing.T) {
	f := New()
	if err := f.AddFrames(9999, []Frame{{Data: []byte("x")}}); err == nil {
		t.Fatal("expected error for unknown configID")
	}
}

func TestStreamParamsValidation(t *testing.T) {
	f := New()
	cases := []struct {
		name   string
		kind   StreamKind
		params StreamParams
	}{
		{"video missing sps", KindVideo, StreamParams{CodecVideo: mediatree.CodecH264, ParamPPS: []byte{1}}},
		{"video missing pps", KindVideo, StreamParams{CodecVideo: mediatree.CodecH264, ParamSPS: []byte{1}}},
		{"video bad codec", KindVideo, StreamParams{CodecVideo: 99, ParamSPS: []byte{1}, ParamPPS: []byte{1}}},
		{"video vps on h264", KindVideo, StreamParams{CodecVideo: mediatree.CodecH264, ParamSPS: []byte{1}, ParamPPS: []byte{1}, ParamVPS: []byte{1}}},
		{"audio missing sample rate", KindAudio, StreamParams{CodecAudio: mediatree.CodecAAC, ChannelCount: 1}},
		{"audio missing channel count", KindAudio, StreamParams{CodecAudio: mediatree.CodecAAC, SampleRate: 8000}},
		{"audio bad codec", KindAudio, StreamParams{CodecAudio: 99, SampleRate: 8000, ChannelCount: 1}},
		{"audio config on non-aac", KindAudio, StreamParams{CodecAudio: mediatree.CodecPCM, SampleRate: 8000, ChannelCount: 1, ParamAudioConfig: []byte{1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.AddStreamParams(1, 0, c.kind, c.params); err == nil {
				t.Errorf("expected validation error for %s", c.name)
			}
		})
	}
}

func TestMultipleChannelsAndStreams(t *testing.T) {
	f := New()
	c1, _ := f.AddStreamParams(1, 0, KindVideo, videoParams())
	c2, _ := f.AddStreamParams(2, 0, KindVideo, videoParams())
	c3, _ := f.AddStreamParams(1, 1, KindAudio, audioParams())
	if c1 == c2 || c1 == c3 || c2 == c3 {
		t.Fatalf("expected distinct config ids, got %d %d %d", c1, c2, c3)
	}
	elems := f.Elements()
	err := mediatree.Validate(elems)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	chID, _ := mediatree.FindChildByRole(elems, 0, mediatree.RoleChannels)
	channels := mediatree.Children(elems, chID)
	if len(channels) != 2 {
		t.Fatalf("got %d channel nodes, want 2", len(channels))
	}
}

// TestConcurrentFillUnderRace is the torture test: many goroutines
// concurrently calling AddStreamParams/AddFrames across many distinct
// (channel,stream) pairs into one Filler, then the frozen result must pass
// mediatree.Validate — run with `go test -race`.
func TestConcurrentFillUnderRace(t *testing.T) {
	const goroutines = 32
	const framesPerGoroutine = 20

	f := New()
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			channel := uint32(g/4 + 1) // several goroutines share a channel
			stream := uint32(g % 4)    // and several share a stream number across channels
			kind := KindVideo
			params := videoParams()
			if g%3 == 0 {
				kind = KindAudio
				params = audioParams()
			}
			configID, err := f.AddStreamParams(channel, stream, kind, params)
			if err != nil {
				t.Errorf("goroutine %d: AddStreamParams: %v", g, err)
				return
			}
			for i := 0; i < framesPerGoroutine; i++ {
				fr := Frame{Data: []byte(fmt.Sprintf("g%d-f%d", g, i)), Time: uint64(i), Kind: mediatree.FrameKindP}
				if i == 0 {
					fr.Kind = mediatree.FrameKindI
				}
				err := f.AddFrames(configID, []Frame{fr})
				if err != nil {
					t.Errorf("goroutine %d: AddFrames: %v", g, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	elems := f.Elements()
	err := mediatree.Validate(elems)
	if err != nil {
		t.Fatalf("Validate after concurrent fill: %v", err)
	}

	// Sanity: every goroutine's frames actually landed (no silent drops).
	wantNodes := goroutines * framesPerGoroutine
	frameNodeCount := 0
	for _, e := range elems {
		if e.Role == mediatree.RoleFrameVideo || e.Role == mediatree.RoleFrameAudio {
			frameNodeCount++
		}
	}
	if frameNodeCount != wantNodes {
		t.Fatalf("got %d frame nodes, want %d", frameNodeCount, wantNodes)
	}
}

func TestContentRoundTripsThroughMediatree(t *testing.T) {
	f := New()
	configID, _ := f.AddStreamParams(7, 0, KindVideo, videoParams())
	_ = f.AddFrames(configID, []Frame{{Data: []byte("x"), Time: 1, Kind: mediatree.FrameKindI}})

	content := f.Content()
	got, err := mediatree.DecodeContent(content)
	if err != nil {
		t.Fatalf("DecodeContent: %v", err)
	}
	err = mediatree.Validate(got)
	if err != nil {
		t.Fatalf("Validate decoded content: %v", err)
	}
	if len(got) != f.Len() {
		t.Fatalf("decoded %d elements, Len() = %d", len(got), f.Len())
	}
}
