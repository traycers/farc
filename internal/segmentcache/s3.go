package segmentcache

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// s3API is the subset of *s3.Client an s3Backend needs — kept as an
// interface (rather than depending on *s3.Client directly) so tests can
// exercise s3Backend against a fake, with no real network/server required.
// *s3.Client satisfies this structurally.
type s3API interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// s3Backend stores segment bytes as objects in an S3-compatible bucket,
// keyed the same way diskBackend paths them: {channel}/{storageID}/
// {uuidHex}/{init.mp4|N.m4s} — so a bucket's contents stay human-navigable
// the same way the disk cache's directory tree is. Only depends on the S3
// API surface, not on any specific product (SeaweedFS's S3 gateway, MinIO,
// AWS S3, Ceph RGW, ... are all interchangeable here).
type s3Backend struct {
	client s3API
	bucket string
}

func newS3Backend(client s3API, bucket string) *s3Backend {
	return &s3Backend{client: client, bucket: bucket}
}

func objectKey(k Key) string {
	return strconv.Itoa(int(k.Channel)) + "/" + k.StorageID + "/" + hex.EncodeToString(k.UUID[:]) + "/" + fileName(k)
}

func (b *s3Backend) get(k Key) ([]byte, bool) {
	out, err := b.client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(objectKey(k)),
	})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, false
		}
		// Any other error (network, auth, ...) is also just a miss from the
		// caller's point of view -- internal/hlsapi's fallback is to rebuild
		// via internal/segment, same as a disk-backend miss.
		return nil, false
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, false
	}
	return data, true
}

func (b *s3Backend) put(k Key, data []byte) error {
	_, err := b.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(objectKey(k)),
		Body:   bytes.NewReader(data),
	})
	return err
}

// delete is unused today (an s3Backend-backed Cache never runs eviction,
// see NewS3's doc comment) but kept so backend's interface stays honest
// about what a store can do — e.g. a future hook that deletes a segment's
// cached bytes when farcd reports its source fcontainer as
// api.EventFblockDeleted.
func (b *s3Backend) delete(k Key) {
	_, _ = b.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(objectKey(k)),
	})
}
