// Package s3 wraps a MinIO/S3-compatible object store and implements the
// objstore.ObjectStore interface (plus a Connector) used by the rest of the
// application. It is the only place, alongside other provider packages, that
// imports a storage SDK. It never downloads whole objects for schema reads:
// text formats stream a bounded prefix, footer formats use ranged random reads.
package s3

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/objstore"
)

// defaultPageSize bounds a page when the caller passes limit<=0. It mirrors the
// explorer's DefaultPageSize (500); it is duplicated here because the objstore
// layer cannot import explorer (explorer depends on objstore, not vice versa).
const defaultPageSize = 500

// Client is a thin wrapper over the MinIO SDK. It implements
// objstore.ObjectStore.
type Client struct {
	mc       *minio.Client
	endpoint string
	region   string
}

var _ objstore.ObjectStore = (*Client)(nil)

// New connects to the object store.
func New(cfg objstore.Config) (*Client, error) {
	var creds *credentials.Credentials
	if cfg.UseRole {
		// Mirror the AWS SDK default chain: env → shared config → IAM role
		// (EC2 IMDS / ECS / IRSA web identity).
		creds = credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.FileAWSCredentials{},
			&credentials.IAM{Client: &http.Client{Timeout: 10 * time.Second}},
		})
	} else {
		creds = credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken)
	}
	opts := &minio.Options{
		Creds:  creds,
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	}
	if cfg.PathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	mc, err := minio.New(cfg.Endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("connect minio: %w", err)
	}
	return &Client{mc: mc, endpoint: cfg.Endpoint, region: cfg.Region}, nil
}

// Endpoint returns the configured endpoint.
func (c *Client) Endpoint() string { return c.endpoint }

// Region returns the configured region.
func (c *Client) Region() string { return c.region }

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

// BucketRegion resolves the region a bucket lives in. Calling it also primes the
// SDK's internal region cache, so subsequent list/get operations sign with the
// correct region even when the client was created without one (AWS auto-detect).
func (c *Client) BucketRegion(ctx context.Context, bucket string) (string, error) {
	return c.mc.GetBucketLocation(ctx, bucket)
}

// WalkObjects streams every object under prefix to fn. Listing is recursive and
// paginated by the SDK, so it scales to millions of objects with constant
// memory. Returning an error from fn stops the walk.
func (c *Client) WalkObjects(ctx context.Context, bucket, prefix, startAfter string, fn func(objstore.ObjectMeta) error) error {
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
		if err := fn(objstore.ObjectMeta{
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
// recurse. When limit > 0 it stops after that many entries and reports
// truncated=true, cancelling the underlying paginated LIST so a folder with
// millions of children returns immediately instead of enumerating them all.
func (c *Client) ListDirectory(ctx context.Context, bucket, prefix string, limit int) (folders []string, files []objstore.ObjectMeta, truncated bool, err error) {
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()

	opts := minio.ListObjectsOptions{Prefix: prefix, Recursive: false}
	for obj := range c.mc.ListObjects(lctx, bucket, opts) {
		if obj.Err != nil {
			if lctx.Err() != nil {
				break // we cancelled after reaching the limit
			}
			return nil, nil, false, obj.Err
		}
		if strings.HasSuffix(obj.Key, "/") {
			if obj.Key != prefix {
				folders = append(folders, obj.Key)
			}
		} else {
			files = append(files, objstore.ObjectMeta{
				Key:          obj.Key,
				ETag:         obj.ETag,
				Size:         obj.Size,
				LastModified: obj.LastModified,
				StorageClass: obj.StorageClass,
				VersionID:    obj.VersionID,
			})
		}
		if limit > 0 && len(folders)+len(files) >= limit {
			truncated = true
			cancel() // stop the SDK's background pagination goroutine
			break
		}
	}
	return folders, files, truncated, nil
}

// ListPage lists one bounded, cursor-resumable page of a prefix's immediate
// children (sub-folders and files, non-recursive), like ListDirectory but with
// an opaque resume cursor.
//
// It maps directly onto S3's ListObjectsV2: the cursor IS the
// ContinuationToken and limit IS MaxKeys, so a single request returns at most
// one page and NextContinuationToken resumes exactly where it left off — even
// across common prefixes (folders). This is why we use the low-level Core API
// rather than the high-level streaming ListObjects: the streaming API hides the
// continuation token and its StartAfter cannot correctly resume a *delimited*
// listing (a folder cursor re-collapses into the same common prefix). A folder
// with millions of children therefore returns immediately, never enumerated.
// When the listing is not truncated, HasMore is false and NextCursor is "".
// limit<=0 uses defaultPageSize; S3 caps MaxKeys at 1000 per request.
func (c *Client) ListPage(ctx context.Context, bucket, prefix, cursor string, limit int) (objstore.PageResult, error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	core := minio.Core{Client: c.mc}
	// startAfter is empty — pagination is driven entirely by the continuation
	// token (cursor). delimiter "/" gives the file-browser (non-recursive) view.
	result, err := core.ListObjectsV2(bucket, prefix, "", cursor, "/", limit)
	if err != nil {
		return objstore.PageResult{}, err
	}

	var res objstore.PageResult
	for _, cp := range result.CommonPrefixes {
		if cp.Prefix == prefix {
			continue // the prefix itself, if echoed back
		}
		res.Folders = append(res.Folders, cp.Prefix)
	}
	for _, obj := range result.Contents {
		if strings.HasSuffix(obj.Key, "/") {
			continue // a folder-placeholder object (key == prefix), not a file
		}
		res.Files = append(res.Files, objstore.ObjectMeta{
			Key:          obj.Key,
			ETag:         obj.ETag,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			StorageClass: obj.StorageClass,
			VersionID:    obj.VersionID,
		})
	}
	res.HasMore = result.IsTruncated
	if res.HasMore {
		res.NextCursor = result.NextContinuationToken
	}
	return res, nil
}

// ListBucketsPage returns one bounded, cursor-resumable page of bucket names.
// S3 has no native bucket pagination — ListBuckets always returns every bucket
// — so paging here is synthetic: fetch all names, sort them, drop names <=
// cursor, and take limit. NextCursor is the last name returned and HasMore is
// true when names remain beyond it. limit<=0 uses defaultPageSize.
func (c *Client) ListBucketsPage(ctx context.Context, cursor string, limit int) (names []string, nextCursor string, hasMore bool, err error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	all, err := c.ListBuckets(ctx)
	if err != nil {
		return nil, "", false, err
	}
	names, nextCursor, hasMore = pageBuckets(all, cursor, limit)
	return names, nextCursor, hasMore, nil
}

// pageBuckets is the pure, synthetic bucket-paging slice: sort names, drop those
// <= cursor, take up to limit, and report whether more remain. Factored out so
// it is unit-testable without a live server (ListBucketsPage supplies the names
// from the SDK). It sorts a copy so the caller's slice is left untouched.
func pageBuckets(all []string, cursor string, limit int) (names []string, nextCursor string, hasMore bool) {
	sorted := make([]string, len(all))
	copy(sorted, all)
	sort.Strings(sorted)
	for _, name := range sorted {
		if name <= cursor {
			continue
		}
		if len(names) >= limit {
			hasMore = true
			break
		}
		names = append(names, name)
	}
	if len(names) > 0 {
		nextCursor = names[len(names)-1]
	}
	return names, nextCursor, hasMore
}

// listPrefix lists objects directly under a prefix (used by table resolvers).
func (c *Client) listPrefix(ctx context.Context, bucket, prefix string) ([]format.Entry, error) {
	var out []format.Entry
	for obj := range c.mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, format.Entry{Key: obj.Key, Size: obj.Size})
	}
	return out, nil
}

// getBytes fetches the full bytes of a (small) object.
func (c *Client) getBytes(ctx context.Context, bucket, key string) ([]byte, error) {
	obj, err := c.mc.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

// Catalog adapts the client to format.Catalog for a specific bucket.
func (c *Client) Catalog(bucket string) format.Catalog { return &catalog{c: c, bucket: bucket} }

type catalog struct {
	c      *Client
	bucket string
}

func (cat *catalog) List(ctx context.Context, prefix string) ([]format.Entry, error) {
	return cat.c.listPrefix(ctx, cat.bucket, prefix)
}
func (cat *catalog) Get(ctx context.Context, key string) ([]byte, error) {
	return cat.c.getBytes(ctx, cat.bucket, key)
}
