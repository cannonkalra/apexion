package s3

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/apexion/apexion/internal/format"
)

const (
	// blockSize is the unit a rangeReader fetches. Format readers (Parquet in
	// particular) issue many small ReadAt calls — the footer length, the
	// footer, then each page — so reading whole aligned blocks turns dozens of
	// tiny ranged GETs into a few.
	blockSize = 1 << 20
	// maxBlocks bounds the blocks a rangeReader keeps (the footer block plus a
	// few data blocks), so its memory stays at a few MiB.
	maxBlocks = 4
)

// Source adapts a single object to format.Source: a streaming GET for text
// formats, and random access via ranged GETs so footer-based formats
// (Parquet/ORC) work without a full download.
type Source struct {
	c      *Client
	bucket string
	key    string
	size   int64
}

// NewSource builds a Source for one object, returned as a format.Source.
func (c *Client) NewSource(bucket, key string, size int64) format.Source {
	return &Source{c: c, bucket: bucket, key: key, size: size}
}

func (s *Source) Key() string { return s.key }
func (s *Source) Size() int64 { return s.size }

// Open returns a fresh streaming reader from the start of the object.
func (s *Source) Open(ctx context.Context) (io.ReadCloser, error) {
	out, err := s.c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(s.key),
	}, s.c.forBucket(ctx, s.bucket))
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

// ReaderAt returns a random-access view backed by ranged GETs of whole blocks.
func (s *Source) ReaderAt(ctx context.Context) (io.ReaderAt, int64, error) {
	return &rangeReader{ctx: ctx, s: s, blocks: map[int64][]byte{}}, s.size, nil
}

// rangeReader implements io.ReaderAt over ranged GETs, caching up to maxBlocks
// aligned blocks. It is safe for concurrent use. Size reports the object size,
// which readers such as parquet-go require to locate the footer.
type rangeReader struct {
	ctx context.Context
	s   *Source

	mu     sync.Mutex
	blocks map[int64][]byte // block index → bytes
	order  []int64          // insertion order, for eviction
}

// Size returns the object size.
func (r *rangeReader) Size() int64 { return r.s.size }

func (r *rangeReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("read %s: negative offset", r.s.key)
	}
	n := 0
	for n < len(p) {
		pos := off + int64(n)
		if pos >= r.s.size {
			return n, io.EOF
		}
		b, err := r.block(pos / blockSize)
		if err != nil {
			return n, err
		}
		n += copy(p[n:], b[pos%blockSize:])
	}
	return n, nil
}

// block returns block i, fetching it with one ranged GET if not cached.
func (r *rangeReader) block(i int64) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.blocks[i]; ok {
		return b, nil
	}
	start := i * blockSize
	end := min(start+blockSize, r.s.size) - 1
	out, err := r.s.c.s3.GetObject(r.ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.s.bucket),
		Key:    aws.String(r.s.key),
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
	}, r.s.c.forBucket(r.ctx, r.s.bucket))
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	b := make([]byte, end-start+1)
	if _, err := io.ReadFull(out.Body, b); err != nil {
		return nil, fmt.Errorf("read %s bytes %d-%d: %w", r.s.key, start, end, err)
	}
	if len(r.order) >= maxBlocks {
		delete(r.blocks, r.order[0])
		r.order = r.order[1:]
	}
	r.blocks[i] = b
	r.order = append(r.order, i)
	return b, nil
}
