package msmapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// recordingServer captures the last request's method/path/body and replies
// with status (and body if status >= 300, mirroring msm's {code,message}
// error shape).
type recordingServer struct {
	*httptest.Server
	method, path string
	body         map[string]any
	status       int
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{status: http.StatusOK}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.method = r.Method
		rs.path = r.URL.RequestURI()
		if r.ContentLength != 0 {
			buf, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(buf, &rs.body)
		}
		if rs.status >= 300 {
			w.WriteHeader(rs.status)
			_ = json.NewEncoder(w).Encode(errorBody{Code: int32(rs.status), Message: "boom"})
			return
		}
		w.WriteHeader(rs.status)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func TestFormatUUID(t *testing.T) {
	var b [16]byte
	for i := range b {
		b[i] = byte(i)
	}
	got := formatUUID(b)
	want := "00010203-0405-0607-0809-0a0b0c0d0e0f"
	if got != want {
		t.Fatalf("formatUUID = %q, want %q", got, want)
	}
}

func TestParamsAdd(t *testing.T) {
	rs := newRecordingServer(t)
	c := New(rs.URL)
	err := c.ParamsAdd(context.Background(), "arch1", 42, 1, map[string]any{"codec": "h264"})
	if err != nil {
		t.Fatalf("ParamsAdd: %v", err)
	}
	if rs.method != http.MethodPost || rs.path != "/api/v1/archives/arch1/streams/params/" {
		t.Fatalf("method/path = %s %s", rs.method, rs.path)
	}
	if rs.body["id"] != float64(42) || rs.body["type"] != float64(1) {
		t.Fatalf("body = %+v", rs.body)
	}
}

func TestFblocksAdd(t *testing.T) {
	rs := newRecordingServer(t)
	c := New(rs.URL)
	var id [16]byte
	id[0] = 0xab
	err := c.FblocksAdd(context.Background(), "arch1", id, 7)
	if err != nil {
		t.Fatalf("FblocksAdd: %v", err)
	}
	if rs.method != http.MethodPost || rs.path != "/api/v1/archives/arch1/fblocks/" {
		t.Fatalf("method/path = %s %s", rs.method, rs.path)
	}
	if rs.body["num"] != float64(7) || !strings.HasPrefix(rs.body["id"].(string), "ab") {
		t.Fatalf("body = %+v", rs.body)
	}
}

func TestFblocksDel(t *testing.T) {
	rs := newRecordingServer(t)
	c := New(rs.URL)
	var id [16]byte
	err := c.FblocksDel(context.Background(), "arch1", id)
	if err != nil {
		t.Fatalf("FblocksDel: %v", err)
	}
	if rs.method != http.MethodDelete || !strings.HasPrefix(rs.path, "/api/v1/archives/arch1/fblocks/?id=") {
		t.Fatalf("method/path = %s %s", rs.method, rs.path)
	}
}

func TestStatusSet(t *testing.T) {
	rs := newRecordingServer(t)
	c := New(rs.URL)
	var fbid [16]byte
	err := c.StatusSet(context.Background(), "arch1", fbid, 2)
	if err != nil {
		t.Fatalf("StatusSet: %v", err)
	}
	if rs.method != http.MethodPut || rs.body["status"] != float64(2) {
		t.Fatalf("method/body = %s %+v", rs.method, rs.body)
	}
}

func TestInfoSet(t *testing.T) {
	rs := newRecordingServer(t)
	c := New(rs.URL)
	var fbid [16]byte
	start := time.Unix(0, 1000)
	stop := time.Unix(0, 2000)
	err := c.InfoSet(context.Background(), "arch1", fbid, 5, 2, start, stop)
	if err != nil {
		t.Fatalf("InfoSet: %v", err)
	}
	if rs.method != http.MethodPut || rs.body["num"] != float64(5) || rs.body["status"] != float64(2) {
		t.Fatalf("method/body = %s %+v", rs.method, rs.body)
	}
	if rs.body["start"] == "" || rs.body["stop"] == "" {
		t.Fatalf("body = %+v", rs.body)
	}
}

func TestStartedAddFinishedAdd(t *testing.T) {
	rs := newRecordingServer(t)
	c := New(rs.URL)
	begin := time.Unix(0, 1000)
	err := c.StartedAdd(context.Background(), "arch1", 3, begin)
	if err != nil {
		t.Fatalf("StartedAdd: %v", err)
	}
	if rs.path != "/api/v1/archives/arch1/channels/3/recording/started/" {
		t.Fatalf("path = %s", rs.path)
	}

	end := time.Unix(0, 2000)
	err = c.FinishedAdd(context.Background(), "arch1", 3, end)
	if err != nil {
		t.Fatalf("FinishedAdd: %v", err)
	}
	if rs.path != "/api/v1/archives/arch1/channels/3/recording/finished/" {
		t.Fatalf("path = %s", rs.path)
	}
}

func TestVaaBlocksAdd(t *testing.T) {
	rs := newRecordingServer(t)
	c := New(rs.URL)
	var fbid [16]byte
	block := VaaBlock{
		ID:         VaaBlockID{Fnum: 1, Offset: 100, Size: 200},
		FblockID:   fbid,
		ChannelNum: 1,
		ParamsID:   9,
		StreamID:   0,
		StreamType: 1,
		Begin:      time.Unix(0, 1000),
		End:        time.Unix(0, 2000),
	}
	err := c.VaaBlocksAdd(context.Background(), "arch1", block)
	if err != nil {
		t.Fatalf("VaaBlocksAdd: %v", err)
	}
	if rs.path != "/api/v1/archives/arch1/vaa_blocks/" {
		t.Fatalf("path = %s", rs.path)
	}
	if rs.body["archive_id"] != "arch1" || rs.body["channel_num"] != float64(1) || rs.body["params_id"] != float64(9) {
		t.Fatalf("body = %+v", rs.body)
	}
}

func TestErrorResponseDecodesMSMShape(t *testing.T) {
	rs := newRecordingServer(t)
	rs.status = http.StatusInternalServerError
	c := New(rs.URL)
	var id [16]byte
	err := c.FblocksAdd(context.Background(), "arch1", id, 1)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "code 500") {
		t.Fatalf("error = %v", err)
	}
}
