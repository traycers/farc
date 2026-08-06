package segmentcache_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"traycers/farc/internal/segmentcache"
)

// fakeS3 is a minimal in-memory stand-in for *s3.Client, satisfying
// NewS3's s3API parameter structurally (no real network/server needed) --
// keyed by *input.Key, mirroring how s3Backend actually addresses objects.
type fakeS3 struct {
	objects map[string][]byte
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: make(map[string][]byte)} }

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	data, ok := f.objects[*in.Key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.objects[*in.Key] = data
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	delete(f.objects, *in.Key)
	return &s3.DeleteObjectOutput{}, nil
}

func TestS3Cache_PutGetRoundTrip(t *testing.T) {
	c := segmentcache.NewS3(newFakeS3(), "test-bucket")
	key := segmentcache.InitKey(1, "s1", [16]byte{1})
	err := c.Put(key, []byte("hello"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := c.Get(key)
	if !ok || !bytes.Equal(got, []byte("hello")) {
		t.Fatalf("Get() = (%q, %v), want (\"hello\", true)", got, ok)
	}
}

func TestS3Cache_GetMiss(t *testing.T) {
	c := segmentcache.NewS3(newFakeS3(), "test-bucket")
	if _, ok := c.Get(segmentcache.MediaKey(1, "s1", [16]byte{9}, 0)); ok {
		t.Fatalf("Get() on an empty cache = found, want miss")
	}
}

// TestS3Cache_NeverEvicts is the whole point of dropping LRU/quota tracking
// for this backend (see Cache's doc comment): unlike the disk-backed
// TestCache_EvictsLeastRecentlyUsedUnderQuota, every key ever Put stays
// retrievable -- there is no quota parameter to NewS3 at all, and space
// bounding is the bucket's own lifecycle policy, not this package's.
func TestS3Cache_NeverEvicts(t *testing.T) {
	c := segmentcache.NewS3(newFakeS3(), "test-bucket")
	keys := make([]segmentcache.Key, 50)
	for i := range keys {
		keys[i] = segmentcache.MediaKey(1, "s1", [16]byte{byte(i)}, 0)
		err := c.Put(keys[i], bytes.Repeat([]byte{0xAB}, 1024))
		if err != nil {
			t.Fatalf("Put(%d): %v", i, err)
		}
	}
	for i, k := range keys {
		if _, ok := c.Get(k); !ok {
			t.Fatalf("Get(keys[%d]) = miss, want hit (an S3-backed Cache must never evict)", i)
		}
	}
}

func TestS3Cache_DifferentChannelsSameUUIDDoNotCollide(t *testing.T) {
	c := segmentcache.NewS3(newFakeS3(), "test-bucket")
	uuid := [16]byte{9}
	channelA := segmentcache.InitKey(1, "s1", uuid)
	channelB := segmentcache.InitKey(2, "s1", uuid)

	err := c.Put(channelA, []byte("channel-1-init"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := c.Get(channelB); ok {
		t.Fatalf("Get(channelB) = hit, want miss (channel 1's cached init segment must not be served for channel 2)")
	}
}
