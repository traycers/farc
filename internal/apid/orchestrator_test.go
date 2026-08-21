package apid_test

import (
	"context"
	"errors"
	"testing"

	"github.com/traycers/farc/internal/apid"
)

type fakeFarcdClient struct {
	channels  map[uint16]apid.ChannelInfo
	createErr error
	updateErr error
	removeErr error
	listErr   error

	createCalls []apid.CreateChannelRequest
	updateCalls []apid.UpdateChannelRequest
	removeCalls []uint16
}

func (f *fakeFarcdClient) ListChannels(_ context.Context) ([]apid.ChannelInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]apid.ChannelInfo, 0, len(f.channels))
	for _, c := range f.channels {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeFarcdClient) CreateChannel(_ context.Context, req apid.CreateChannelRequest) (apid.ChannelInfo, error) {
	f.createCalls = append(f.createCalls, req)
	if f.createErr != nil {
		return apid.ChannelInfo{}, f.createErr
	}
	info := apid.ChannelInfo{Channel: req.ID, RTSPURL: req.RTSPURL, Storage: req.Storage, PolicyType: req.CapturePolicyType, Name: req.Name}
	if f.channels == nil {
		f.channels = map[uint16]apid.ChannelInfo{}
	}
	f.channels[req.ID] = info
	return info, nil
}

func (f *fakeFarcdClient) UpdateChannel(_ context.Context, id uint16, req apid.UpdateChannelRequest) (apid.ChannelInfo, error) {
	f.updateCalls = append(f.updateCalls, req)
	if f.updateErr != nil {
		return apid.ChannelInfo{}, f.updateErr
	}
	info := apid.ChannelInfo{Channel: id, RTSPURL: req.RTSPURL, Storage: req.Storage, PolicyType: req.CapturePolicyType, Name: req.Name}
	if f.channels == nil {
		f.channels = map[uint16]apid.ChannelInfo{}
	}
	f.channels[id] = info
	return info, nil
}

func (f *fakeFarcdClient) RemoveChannel(_ context.Context, id uint16) error {
	f.removeCalls = append(f.removeCalls, id)
	if f.removeErr != nil {
		return f.removeErr
	}
	delete(f.channels, id)
	return nil
}

type fakeMediamtxClient struct {
	paths     map[string]string
	existsErr error
	addErr    error
	patchErr  error
	removeErr error

	addCalls    []string
	patchCalls  []string
	deleteCalls []string
}

func (f *fakeMediamtxClient) PathExists(_ context.Context, name string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	_, ok := f.paths[name]
	return ok, nil
}

func (f *fakeMediamtxClient) GetPathSource(_ context.Context, name string) (string, bool, error) {
	if f.existsErr != nil {
		return "", false, f.existsErr
	}
	source, ok := f.paths[name]
	return source, ok, nil
}

func (f *fakeMediamtxClient) AddPath(_ context.Context, name, source string) error {
	f.addCalls = append(f.addCalls, name)
	if f.addErr != nil {
		return f.addErr
	}
	if f.paths == nil {
		f.paths = map[string]string{}
	}
	f.paths[name] = source
	return nil
}

func (f *fakeMediamtxClient) PatchPath(_ context.Context, name, source string) error {
	f.patchCalls = append(f.patchCalls, name)
	if f.patchErr != nil {
		return f.patchErr
	}
	if f.paths == nil {
		f.paths = map[string]string{}
	}
	f.paths[name] = source
	return nil
}

func (f *fakeMediamtxClient) DeletePath(_ context.Context, name string) error {
	f.deleteCalls = append(f.deleteCalls, name)
	if f.removeErr != nil {
		return f.removeErr
	}
	delete(f.paths, name)
	return nil
}

const (
	rtspBase = "rtsp://mediamtx:8554"
	whepBase = "http://mediamtx:8889"
)

func TestOrchestrator_CreateChannel_FreshChannel(t *testing.T) {
	farcd := &fakeFarcdClient{}
	mtx := &fakeMediamtxClient{}
	o := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)

	result := o.CreateChannel(context.Background(), apid.ChannelWriteRequest{
		ID: 1, CameraRTSPURL: "rtsp://camera/1", Storage: "s1", CapturePolicyType: "continuous", Name: "front door",
	})

	if !result.FarcdOK || result.FarcdErr != nil {
		t.Fatalf("FarcdOK/FarcdErr = %v, %v", result.FarcdOK, result.FarcdErr)
	}
	if !result.MediamtxOK || result.MediamtxErr != nil {
		t.Fatalf("MediamtxOK/MediamtxErr = %v, %v", result.MediamtxOK, result.MediamtxErr)
	}
	if len(farcd.createCalls) != 1 {
		t.Fatalf("farcd create calls = %d, want 1", len(farcd.createCalls))
	}
	if got := farcd.createCalls[0].RTSPURL; got != "rtsp://mediamtx:8554/1" {
		t.Fatalf("farcd create rtsp_url = %q, want mediamtx re-serve URL, not the camera URL", got)
	}
	if len(mtx.addCalls) != 1 || mtx.paths["1"] != "rtsp://camera/1" {
		t.Fatalf("mediamtx add calls = %+v, paths = %+v", mtx.addCalls, mtx.paths)
	}
	if len(mtx.patchCalls) != 0 {
		t.Fatalf("mediamtx patch calls = %+v, want none for a fresh path", mtx.patchCalls)
	}
}

func TestOrchestrator_CreateChannel_IdempotentWhenFarcdChannelAlreadyExists(t *testing.T) {
	farcd := &fakeFarcdClient{channels: map[uint16]apid.ChannelInfo{1: {Channel: 1}}}
	mtx := &fakeMediamtxClient{}
	o := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)

	result := o.CreateChannel(context.Background(), apid.ChannelWriteRequest{
		ID: 1, CameraRTSPURL: "rtsp://camera/1", Storage: "s1", CapturePolicyType: "continuous",
	})

	if !result.FarcdOK || result.FarcdErr != nil {
		t.Fatalf("FarcdOK/FarcdErr = %v, %v", result.FarcdOK, result.FarcdErr)
	}
	if len(farcd.createCalls) != 0 {
		t.Fatalf("farcd create calls = %d, want 0 (already exists -- retry must be a no-op)", len(farcd.createCalls))
	}
}

func TestOrchestrator_CreateChannel_PatchesExistingMediamtxPath(t *testing.T) {
	farcd := &fakeFarcdClient{}
	mtx := &fakeMediamtxClient{paths: map[string]string{"1": "rtsp://camera/old"}}
	o := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)

	result := o.CreateChannel(context.Background(), apid.ChannelWriteRequest{
		ID: 1, CameraRTSPURL: "rtsp://camera/1", Storage: "s1", CapturePolicyType: "continuous",
	})

	if !result.MediamtxOK || result.MediamtxErr != nil {
		t.Fatalf("MediamtxOK/MediamtxErr = %v, %v", result.MediamtxOK, result.MediamtxErr)
	}
	if len(mtx.addCalls) != 0 || len(mtx.patchCalls) != 1 {
		t.Fatalf("add calls = %d, patch calls = %d, want 0 add / 1 patch for an existing path", len(mtx.addCalls), len(mtx.patchCalls))
	}
}

func TestOrchestrator_CreateChannel_PartialFailureIsIndependentPerSide(t *testing.T) {
	farcd := &fakeFarcdClient{createErr: errors.New("storage full")}
	mtx := &fakeMediamtxClient{}
	o := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)

	result := o.CreateChannel(context.Background(), apid.ChannelWriteRequest{
		ID: 1, CameraRTSPURL: "rtsp://camera/1", Storage: "full-storage", CapturePolicyType: "continuous",
	})

	if result.FarcdOK || result.FarcdErr == nil {
		t.Fatalf("FarcdOK/FarcdErr = %v, %v, want failure surfaced", result.FarcdOK, result.FarcdErr)
	}
	if !result.MediamtxOK || result.MediamtxErr != nil {
		t.Fatalf("MediamtxOK/MediamtxErr = %v, %v, want mediamtx side to still succeed independently", result.MediamtxOK, result.MediamtxErr)
	}
}

func TestOrchestrator_UpdateChannel(t *testing.T) {
	farcd := &fakeFarcdClient{channels: map[uint16]apid.ChannelInfo{1: {Channel: 1}}}
	mtx := &fakeMediamtxClient{paths: map[string]string{"1": "rtsp://camera/old"}}
	o := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)

	result := o.UpdateChannel(context.Background(), 1, apid.ChannelWriteRequest{
		CameraRTSPURL: "rtsp://camera/1-new", Storage: "s1", CapturePolicyType: "continuous", Name: "renamed",
	})

	if !result.FarcdOK || result.FarcdErr != nil || !result.MediamtxOK || result.MediamtxErr != nil {
		t.Fatalf("result = %+v", result)
	}
	if len(farcd.updateCalls) != 1 || farcd.updateCalls[0].RTSPURL != "rtsp://mediamtx:8554/1" || farcd.updateCalls[0].Name != "renamed" {
		t.Fatalf("farcd update calls = %+v", farcd.updateCalls)
	}
	if mtx.paths["1"] != "rtsp://camera/1-new" {
		t.Fatalf("mediamtx path source = %q, want updated camera URL", mtx.paths["1"])
	}
}

func TestOrchestrator_RemoveChannel(t *testing.T) {
	farcd := &fakeFarcdClient{channels: map[uint16]apid.ChannelInfo{1: {Channel: 1}}}
	mtx := &fakeMediamtxClient{paths: map[string]string{"1": "rtsp://camera/1"}}
	o := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)

	result := o.RemoveChannel(context.Background(), 1)

	if !result.FarcdOK || result.FarcdErr != nil || !result.MediamtxOK || result.MediamtxErr != nil {
		t.Fatalf("result = %+v", result)
	}
	if len(farcd.removeCalls) != 1 || farcd.removeCalls[0] != 1 {
		t.Fatalf("farcd remove calls = %+v", farcd.removeCalls)
	}
	if len(mtx.deleteCalls) != 1 || mtx.deleteCalls[0] != "1" {
		t.Fatalf("mediamtx delete calls = %+v", mtx.deleteCalls)
	}
}

func TestOrchestrator_GetCameraURL(t *testing.T) {
	farcd := &fakeFarcdClient{}
	mtx := &fakeMediamtxClient{paths: map[string]string{"1": "rtsp://camera/1"}}
	o := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)

	source, exists, err := o.GetCameraURL(context.Background(), 1)
	if err != nil || !exists || source != "rtsp://camera/1" {
		t.Fatalf("GetCameraURL(1) = %q, %v, %v", source, exists, err)
	}

	source, exists, err = o.GetCameraURL(context.Background(), 2)
	if err != nil || exists || source != "" {
		t.Fatalf("GetCameraURL(2) = %q, %v, %v, want not-found", source, exists, err)
	}
}

func TestOrchestrator_GetLiveURLs(t *testing.T) {
	farcd := &fakeFarcdClient{}
	mtx := &fakeMediamtxClient{}
	o := apid.NewOrchestrator(farcd, mtx, rtspBase, whepBase)

	urls := o.GetLiveURLs([]uint16{1, 2})

	if urls[1] != "http://mediamtx:8889/1/whep" || urls[2] != "http://mediamtx:8889/2/whep" {
		t.Fatalf("urls = %+v", urls)
	}
	if len(mtx.addCalls)+len(mtx.patchCalls)+len(mtx.deleteCalls) != 0 {
		t.Fatalf("GetLiveURLs must not call mediamtx at all -- the URL is fully derivable from config + id")
	}
}
