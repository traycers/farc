package hlsapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/traycers/farc/internal/hlsapi"
	"github.com/traycers/farc/internal/hlsclient"
	"github.com/traycers/farc/internal/segmentcache"
	"github.com/traycers/farc/internal/tocindex"
	"github.com/traycers/farc/mediatree"
)

type timelineSegmentDTO struct {
	Begin uint64 `json:"begin"`
	End   uint64 `json:"end"`
}

type channelTimelineDTO struct {
	Channel  uint16               `json:"channel"`
	Segments []timelineSegmentDTO `json:"segments"`
}

// TestServer_Timeline_MultiChannelBatchQuery is .scratch/player-redesign/
// issues/01-hls-server-timeline-endpoint.md's HTTP seam: one request for a
// list of channels returns each one's precomputed video-presence timeline,
// clipped to [t1,t2], with unconfigured channels silently omitted rather
// than failing the whole batch.
func TestServer_Timeline_MultiChannelBatchQuery(t *testing.T) {
	unit := newTestUnit(t)

	videoFrames1 := []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xaa}},
		{Time: 1_000_000_000, Kind: mediatree.FrameKindP, NAL: []byte{0x41}},
	}
	uuid1 := writeAVFcontainer(t, unit, 1, videoFrames1, nil, 0, 1_000_000_000, 1000)

	videoFrames2 := []videoFrameSpec{
		{Time: 0, Kind: mediatree.FrameKindI, NAL: []byte{0x65, 0xbb}},
	}
	uuid2 := writeAVFcontainer(t, unit, 2, videoFrames2, nil, 0, 0, 1001)

	farcd := newFarcdTestServer(t, unit)
	client := hlsclient.New(farcd.URL, farcd.wsURL)

	columns1, err := client.GetTOC(context.Background(), "s1", uuid1)
	if err != nil {
		t.Fatalf("GetTOC(1): %v", err)
	}
	columns2, err := client.GetTOC(context.Background(), "s1", uuid2)
	if err != nil {
		t.Fatalf("GetTOC(2): %v", err)
	}

	idx := tocindex.NewIndex()
	idx.Channel(1).Insert(tocindex.Record{
		UUID: uuid1, StorageID: "s1", Begin: 0, End: 1_000_000_000, Columns: columns1,
		VideoSegments: tocindex.VideoPresenceSegments(columns1, 1),
	})
	idx.Channel(2).Insert(tocindex.Record{
		UUID: uuid2, StorageID: "s1", Begin: 0, End: 0, Columns: columns2,
		VideoSegments: tocindex.VideoPresenceSegments(columns2, 2),
	})

	cache, err := segmentcache.New(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("segmentcache.New: %v", err)
	}
	// channel 3 is deliberately not in this configured set, to prove it gets
	// silently omitted rather than 404ing (or erroring) the whole batch.
	srv := hlsapi.New(idx, client, map[uint16]bool{1: true, 2: true}, cache, 10*time.Millisecond)
	hls := httptest.NewServer(srv.Handler())
	defer hls.Close()

	status, body := mustGet(t, hls.URL+"/timeline?channels=1,2,3&t1=0&t2=1000000000")
	if status != http.StatusOK {
		t.Fatalf("timeline status = %d, body: %s", status, body)
	}

	var got []channelTimelineDTO
	err = json.Unmarshal(body, &got)
	if err != nil {
		t.Fatalf("unmarshal response %s: %v", body, err)
	}

	want := []channelTimelineDTO{
		{Channel: 1, Segments: []timelineSegmentDTO{{Begin: 0, End: 1_000_000_000}}},
		{Channel: 2, Segments: []timelineSegmentDTO{{Begin: 0, End: 0}}},
	}
	if len(got) != len(want) {
		t.Fatalf("timeline response = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Channel != want[i].Channel {
			t.Fatalf("timeline response[%d].Channel = %d, want %d (full: %+v)", i, got[i].Channel, want[i].Channel, got)
		}
		if len(got[i].Segments) != len(want[i].Segments) || got[i].Segments[0] != want[i].Segments[0] {
			t.Fatalf("timeline response[%d].Segments = %+v, want %+v (full: %+v)", i, got[i].Segments, want[i].Segments, got)
		}
	}
}
