// Package s3 wraps a MinIO/S3-compatible object store and adapts objects to the
// format.Source and format.Catalog interfaces used by the readers and
// table-format resolvers. It never downloads whole objects for schema reads:
// text formats stream a bounded prefix, footer formats use ranged random reads.
package s3

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/apexion/apexion/internal/crawler/format"
)

// Provider returns the currently-active S3 client. It lets long-lived services
// (crawler, explorer, catalog) follow the user's active connection without
// being rebuilt when it changes.
type Provider interface {
	Client() *Client
}

// Client is a thin wrapper over the MinIO SDK.
type Client struct {
	mc       *minio.Client
	endpoint string
	region   string
}

// Config holds connection settings.
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Region    string
}

// New connects to the object store.
func New(cfg Config) (*Client, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("connect minio: %w", err)
	}
	return &Client{mc: mc, endpoint: cfg.Endpoint, region: cfg.Region}, nil
}

// Endpoint returns the configured endpoint.
func (c *Client) Endpoint() string { return c.endpoint }

// Region returns the configured region.
func (c *Client) Region() string { return c.region }

// ObjectMeta is a lightweight listing record.
type ObjectMeta struct {
	Key          string
	ETag         string
	Size         int64
	LastModified time.Time
	StorageClass string
	VersionID    string
}

// ListBuckets returns the names of all buckets.
func (c *Client) ListBuckets(ctx context.Context) ([]string, error) {
	infos, err := c.mc.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, b := range infos {
		names[i] = b.Name
	}
	return names, nil
}

// BucketExists reports whether a bucket exists.
func (c *Client) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return c.mc.BucketExists(ctx, bucket)
}

// WalkObjects streams every object under prefix to fn. Listing is recursive and
// paginated by the SDK, so it scales to millions of objects with constant
// memory. Returning an error from fn stops the walk.
func (c *Client) WalkObjects(ctx context.Context, bucket, prefix string, startAfter string, fn func(ObjectMeta) error) error {
	opts := minio.ListObjectsOptions{
		Prefix:       prefix,
		Recursive:    true,
		StartAfter:   startAfter,
		WithMetadata: false,
	}
	for obj := range c.mc.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return obj.Err
		}
		if err := fn(ObjectMeta{
			Key:          obj.Key,
			ETag:         obj.ETag,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			StorageClass: obj.StorageClass,
			VersionID:    obj.VersionID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ListDirectory lists the immediate children of a prefix using a delimiter,
// like a file browser: sub-folders (common prefixes) and files. It does not
// recurse, so it is O(children) regardless of total object count.
func (c *Client) ListDirectory(ctx context.Context, bucket, prefix string) (folders []string, files []ObjectMeta, err error) {
	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: false}
	for obj := range c.mc.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return nil, nil, obj.Err
		}
		if strings.HasSuffix(obj.Key, "/") {
			if obj.Key != prefix {
				folders = append(folders, obj.Key)
			}
			continue
		}
		files = append(files, ObjectMeta{
			Key:          obj.Key,
			ETag:         obj.ETag,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			StorageClass: obj.StorageClass,
			VersionID:    obj.VersionID,
		})
	}
	return folders, files, nil
}

// ListPrefix lists objects directly under a prefix (used by table resolvers).
func (c *Client) ListPrefix(ctx context.Context, bucket, prefix string) ([]format.Entry, error) {
	var out []format.Entry
	for obj := range c.mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, format.Entry{Key: obj.Key, Size: obj.Size})
	}
	return out, nil
}

// GetBytes fetches the full bytes of a (small) object.
func (c *Client) GetBytes(ctx context.Context, bucket, key string) ([]byte, error) {
	obj, err := c.mc.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

// ReadHeader reads up to n bytes from the start of an object (for magic-byte
// format detection) without downloading the whole object.
func (c *Client) ReadHeader(ctx context.Context, bucket, key string, n int) ([]byte, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(0, int64(n-1)); err != nil {
		return nil, err
	}
	obj, err := c.mc.GetObject(ctx, bucket, key, opts)
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	buf := make([]byte, n)
	read, err := io.ReadFull(obj, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:read], nil
}

// Catalog adapts the client to format.Catalog for a specific bucket.
func (c *Client) Catalog(bucket string) format.Catalog { return &catalog{c: c, bucket: bucket} }

type catalog struct {
	c      *Client
	bucket string
}

func (cat *catalog) List(ctx context.Context, prefix string) ([]format.Entry, error) {
	return cat.c.ListPrefix(ctx, cat.bucket, prefix)
}
func (cat *catalog) Get(ctx context.Context, key string) ([]byte, error) {
	return cat.c.GetBytes(ctx, cat.bucket, key)
}
