package s3

import (
	"context"
	"io"

	"github.com/minio/minio-go/v7"

	"github.com/apexion/apexion/internal/format"
)

// Source adapts a single object to format.Source. minio.Object natively
// supports both streaming reads and random access (io.ReaderAt via ranged GET),
// so footer-based formats (Parquet/ORC) work without a full download.
type Source struct {
	mc     *minio.Client
	bucket string
	key    string
	size   int64
}

// NewSource builds a Source for one object, returned as a format.Source.
func (c *Client) NewSource(bucket, key string, size int64) format.Source {
	return &Source{mc: c.mc, bucket: bucket, key: key, size: size}
}

func (s *Source) Key() string { return s.key }
func (s *Source) Size() int64 { return s.size }

// Open returns a fresh streaming reader from the start of the object.
func (s *Source) Open(ctx context.Context) (io.ReadCloser, error) {
	return s.mc.GetObject(ctx, s.bucket, s.key, minio.GetObjectOptions{})
}

// ReaderAt returns a random-access view backed by ranged GETs.
func (s *Source) ReaderAt(ctx context.Context) (io.ReaderAt, int64, error) {
	obj, err := s.mc.GetObject(ctx, s.bucket, s.key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	return obj, s.size, nil
}
