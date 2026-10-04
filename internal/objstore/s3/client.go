// Package s3 wraps an S3-compatible object store with the AWS SDK for Go v2 and
// implements the objstore.ObjectStore interface (plus a Connector) used by the
// rest of the application. It is the only place, alongside other provider
// packages, that imports a storage SDK. It never downloads whole objects for
// schema reads: text formats stream a bounded prefix, footer formats use ranged
// random reads.
package s3

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/objstore"
)

// defaultPageSize bounds a page when the caller passes limit<=0. It mirrors the
// explorer's DefaultPageSize (500); it is duplicated here because the objstore
// layer cannot import explorer (explorer depends on objstore, not vice versa).
const defaultPageSize = 500

// maxKeys is S3's cap on keys per ListObjectsV2 request.
const maxKeys = 1000

// defaultRegion signs requests when no region is configured, and is the
// partition hint for discovering an AWS bucket's region.
const defaultRegion = "us-east-1"

// awsDefaultEndpoint matches AWS's standard S3 hostnames, which the SDK resolves
// itself (per region). Any other amazonaws.com host — e.g. a VPC interface
// endpoint — is used as given.
var awsDefaultEndpoint = regexp.MustCompile(`^(https?://)?s3([.-][a-z0-9-]+)?\.amazonaws\.com(\.cn)?/?$`)

// Client is a thin wrapper over the AWS SDK S3 client. It implements
// objstore.ObjectStore.
type Client struct {
	s3       *s3.Client
	endpoint string
	region   string
	// discover is set for AWS connections without a region: each bucket's real
	// region is looked up once (HeadBucket) and requests for it are signed for
	// that region, since a bucket outside the signing region rejects them.
	discover bool
	regions  sync.Map // bucket → region, when discover
}

var _ objstore.ObjectStore = (*Client)(nil)

// New builds a client for the object store. It does no network I/O.
func New(cfg objstore.Config) (*Client, error) {
	ctx := context.Background()
	region := cfg.Region
	discover := region == "" && isAWS(cfg)
	if region == "" {
		region = defaultRegion
	}

	// Checksums are only computed/validated when an operation requires them:
	// this client only reads, and many S3-compatible stores (and ranged GETs)
	// do not return the newer flexible checksums.
	var awsCfg aws.Config
	if cfg.UseRole {
		// The AWS SDK default chain: env → shared config/credentials → SSO →
		// web identity (IRSA) → ECS → EC2 instance role (IMDS).
		var err error
		awsCfg, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
			config.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
		)
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
	} else {
		// Static keys: build the config directly so nothing is read from the
		// environment or ~/.aws (the preview engine sets AWS_* env vars for
		// DuckDB's table-format readers).
		awsCfg = aws.Config{
			Region:                     region,
			Credentials:                aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken)),
			RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
			ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		}
	}

	baseEndpoint := endpointURL(cfg)
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = usePathStyle(cfg)
		// Always set explicitly: an env AWS_ENDPOINT_URL(_S3) — which the preview
		// engine may have set for a previous connection — must not redirect
		// this client. nil means AWS's own regional endpoints.
		o.BaseEndpoint = baseEndpoint
	})
	return &Client{s3: client, endpoint: cfg.Endpoint, region: cfg.Region, discover: discover}, nil
}

// usePathStyle reports whether requests address buckets in the path
// (host/bucket/key) rather than the hostname (bucket.host/key). Only AWS uses
// virtual-hosted addressing by default: S3-compatible stores (MinIO,
// SeaweedFS, …) are addressed path-style, as most need it — SeaweedFS answers
// a virtual-hosted LIST with an empty, successful result.
func usePathStyle(cfg objstore.Config) bool {
	return cfg.PathStyle || !isAWS(cfg)
}

// endpointURL is the base endpoint URL for the SDK, or nil to let it resolve
// AWS's standard regional endpoints.
func endpointURL(cfg objstore.Config) *string {
	ep := strings.TrimSpace(cfg.Endpoint)
	if ep == "" || (isAWS(cfg) && awsDefaultEndpoint.MatchString(ep)) {
		return nil
	}
	if !strings.Contains(ep, "://") {
		scheme := "http://"
		if cfg.UseSSL {
			scheme = "https://"
		}
		ep = scheme + ep
	}
	return aws.String(ep)
}

// Endpoint returns the configured endpoint.
func (c *Client) Endpoint() string { return c.endpoint }

// Region returns the configured region.
func (c *Client) Region() string { return c.region }

// forBucket signs a request for the bucket's region when it is discovered per
// bucket. A failed lookup leaves the default region, so the operation itself
// reports the real error (e.g. NoSuchBucket, AccessDenied).
func (c *Client) forBucket(ctx context.Context, bucket string) func(*s3.Options) {
	return func(o *s3.Options) {
		if !c.discover {
			return
		}
		if r, err := c.bucketRegion(ctx, bucket); err == nil && r != "" {
			o.Region = r
		}
	}
}

func (c *Client) bucketRegion(ctx context.Context, bucket string) (string, error) {
	if r, ok := c.regions.Load(bucket); ok {
		return r.(string), nil
	}
	r, err := manager.GetBucketRegion(ctx, c.s3, bucket)
	if err != nil {
		return "", err
	}
	c.regions.Store(bucket, r)
	return r, nil
}

// ListBuckets returns the names of all buckets.
func (c *Client) ListBuckets(ctx context.Context) ([]string, error) {
	var names []string
	p := s3.NewListBucketsPaginator(c.s3, &s3.ListBucketsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, b := range page.Buckets {
			names = append(names, aws.ToString(b.Name))
		}
	}
	return names, nil
}

// BucketRegion resolves the region a bucket lives in. For AWS connections
// without a configured region it is looked up once per bucket (and cached, so
// later requests are signed for it); otherwise it is the configured region.
func (c *Client) BucketRegion(ctx context.Context, bucket string) (string, error) {
	if c.discover {
		return c.bucketRegion(ctx, bucket)
	}
	if c.region != "" {
		return c.region, nil
	}
	return defaultRegion, nil
}

// WalkObjects streams every object under prefix to fn. Listing is recursive and
// paginated (1,000 keys per request), so it scales to millions of objects with
// constant memory. Returning an error from fn stops the walk.
func (c *Client) WalkObjects(ctx context.Context, bucket, prefix, startAfter string, fn func(objstore.ObjectMeta) error) error {
	in := &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix)}
	if startAfter != "" {
		in.StartAfter = aws.String(startAfter)
	}
	p := s3.NewListObjectsV2Paginator(c.s3, in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx, c.forBucket(ctx, bucket))
		if err != nil {
			return err
		}
		for _, o := range page.Contents {
			if err := fn(objectMeta(o)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListDirectory lists the immediate children of a prefix using a delimiter,
// like a file browser: sub-folders (common prefixes) and files, in key order.
// It does not recurse. When limit > 0 it stops after that many entries and
// reports truncated=true, without fetching further pages, so a folder with
// millions of children returns immediately.
func (c *Client) ListDirectory(ctx context.Context, bucket, prefix string, limit int) (folders []string, files []objstore.ObjectMeta, truncated bool, err error) {
	p := s3.NewListObjectsV2Paginator(c.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket), Prefix: aws.String(prefix), Delimiter: aws.String("/"),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx, c.forBucket(ctx, bucket))
		if err != nil {
			return nil, nil, false, err
		}
		// A page holds folders and files separately, each sorted; merge them so
		// the limit keeps the lexicographically first entries.
		cps, objs := page.CommonPrefixes, page.Contents
		for len(cps) > 0 || len(objs) > 0 {
			if len(cps) > 0 && (len(objs) == 0 || aws.ToString(cps[0].Prefix) < aws.ToString(objs[0].Key)) {
				if f := aws.ToString(cps[0].Prefix); f != prefix {
					folders = append(folders, f)
				}
				cps = cps[1:]
			} else {
				if k := aws.ToString(objs[0].Key); !strings.HasSuffix(k, "/") {
					files = append(files, objectMeta(objs[0]))
				}
				objs = objs[1:]
			}
			if limit > 0 && len(folders)+len(files) >= limit {
				return folders, files, true, nil
			}
		}
	}
	return folders, files, false, nil
}

// ListPage lists one bounded, cursor-resumable page of a prefix's immediate
// children (sub-folders and files, non-recursive), like ListDirectory but with
// an opaque resume cursor.
//
// It maps directly onto S3's ListObjectsV2: the cursor IS the
// ContinuationToken and limit IS MaxKeys, so a single request returns at most
// one page and NextContinuationToken resumes exactly where it left off — even
// across common prefixes (folders). StartAfter cannot do this for a delimited
// listing (a folder cursor re-collapses into the same common prefix). A folder
// with millions of children therefore returns immediately, never enumerated.
// When the listing is not truncated, HasMore is false and NextCursor is "".
// limit<=0 uses defaultPageSize; S3 caps MaxKeys at 1000 per request.
func (c *Client) ListPage(ctx context.Context, bucket, prefix, cursor string, limit int) (objstore.PageResult, error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxKeys {
		limit = maxKeys
	}
	in := &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"), // file-browser (non-recursive) view
		MaxKeys:   aws.Int32(int32(limit)),
	}
	if cursor != "" {
		in.ContinuationToken = aws.String(cursor)
	}
	out, err := c.s3.ListObjectsV2(ctx, in, c.forBucket(ctx, bucket))
	if err != nil {
		return objstore.PageResult{}, err
	}

	var res objstore.PageResult
	for _, cp := range out.CommonPrefixes {
		if p := aws.ToString(cp.Prefix); p != prefix { // skip the prefix itself, if echoed back
			res.Folders = append(res.Folders, p)
		}
	}
	for _, o := range out.Contents {
		if strings.HasSuffix(aws.ToString(o.Key), "/") {
			continue // a folder-placeholder object (key == prefix), not a file
		}
		res.Files = append(res.Files, objectMeta(o))
	}
	res.HasMore = aws.ToBool(out.IsTruncated)
	if res.HasMore {
		res.NextCursor = aws.ToString(out.NextContinuationToken)
	}
	return res, nil
}

// objectMeta converts a listed object. The ETag is unquoted (S3 returns it in
// quotes), matching what earlier versions stored, so crawl change detection —
// which hashes the ETag — does not see every object as changed.
func objectMeta(o types.Object) objstore.ObjectMeta {
	return objstore.ObjectMeta{
		Key:          aws.ToString(o.Key),
		ETag:         strings.Trim(aws.ToString(o.ETag), `"`),
		Size:         aws.ToInt64(o.Size),
		LastModified: aws.ToTime(o.LastModified),
		StorageClass: string(o.StorageClass),
	}
}

// ListBucketsPage returns one bounded, cursor-resumable page of bucket names.
// Paging here is synthetic — fetch all names, sort them, drop names <= cursor,
// and take limit — so pages are stable and sorted regardless of the backend.
// NextCursor is the last name returned and HasMore is true when names remain
// beyond it. limit<=0 uses defaultPageSize.
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

// listPrefix lists objects under a prefix, recursively (used by table resolvers).
func (c *Client) listPrefix(ctx context.Context, bucket, prefix string) ([]format.Entry, error) {
	var out []format.Entry
	err := c.WalkObjects(ctx, bucket, prefix, "", func(om objstore.ObjectMeta) error {
		out = append(out, format.Entry{Key: om.Key, Size: om.Size})
		return nil
	})
	return out, err
}

// getBytes fetches the full bytes of a (small) object.
func (c *Client) getBytes(ctx context.Context, bucket, key string) ([]byte, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, c.forBucket(ctx, bucket))
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
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
