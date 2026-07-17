package s3

import (
	"context"
	"strings"
	"sync"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/objstore"
)

// store is the objstore.ObjectStore for one S3 connection profile. It owns all
// S3/AWS policy — endpoint/SSL/region normalization, virtual-hosted vs
// path-style addressing, and the DuckDB preview configuration — so nothing
// outside this package needs to know anything about S3 behavior. It lazily
// builds and caches a virtual-hosted and a path-style client and routes each
// request to the right one based on the bucket name.
type store struct {
	cfg objstore.Config
	aws bool

	mu    sync.Mutex
	vhost *Client
	path  *Client
}

var (
	_ objstore.ObjectStore     = (*store)(nil)
	_ objstore.QueryConfigurer = (*store)(nil)
)

func newStore(cfg objstore.Config) *store {
	return &store{cfg: cfg, aws: isAWS(cfg)}
}

// isAWS reports whether a connection targets real AWS S3 (by provider choice or
// an amazonaws.com endpoint), which needs HTTPS and per-bucket region handling.
func isAWS(cfg objstore.Config) bool {
	return cfg.Provider == "aws" || strings.Contains(strings.ToLower(cfg.Endpoint), "amazonaws.com")
}

// needsPathStyle reports whether a bucket name is not DNS-compatible and so
// requires path-style addressing on AWS.
func needsPathStyle(bucket string) bool {
	if bucket == "" {
		return false
	}
	return bucket != strings.ToLower(bucket) || // uppercase
		strings.Contains(bucket, "_") || // underscore
		strings.Contains(bucket, ".") // dots break virtual-host + TLS
}

// clientFor returns the client whose addressing suits the bucket. Legacy AWS
// bucket names (uppercase, underscore, dots) need path-style; the connection
// may also force it.
func (s *store) clientFor(bucket string) *Client {
	pathStyle := s.cfg.PathStyle || (s.aws && needsPathStyle(bucket))
	return s.client(pathStyle)
}

func (s *store) client(pathStyle bool) *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := &s.vhost
	if pathStyle {
		slot = &s.path
	}
	if *slot == nil {
		c, err := New(s.connConfig(pathStyle))
		if err != nil || c == nil {
			// New only fails on a malformed endpoint; fall back to a best-effort
			// client so callers get a consistent (if failing) surface.
			c = &Client{}
		}
		*slot = c
	}
	return *slot
}

// connConfig applies AWS normalization and the chosen addressing to produce the
// low-level client config.
//
// For AWS we (a) force HTTPS — plain HTTP to s3.amazonaws.com returns 307
// redirects — and (b) leave the signing region empty so the SDK auto-discovers
// each bucket's real region (a bucket outside us-east-1 otherwise fails with
// "Access Denied" when signed for the wrong region).
func (s *store) connConfig(pathStyle bool) objstore.Config {
	cfg := s.cfg
	cfg.PathStyle = pathStyle || s.cfg.PathStyle
	if s.aws {
		if cfg.Endpoint == "" {
			cfg.Endpoint = "s3.amazonaws.com"
		}
		cfg.UseSSL = true
		cfg.Region = "" // auto-discover per bucket
		if cfg.AccessKey == "" {
			cfg.UseRole = true // no static keys → instance/service role
		}
	}
	return cfg
}

// ---- objstore.ObjectStore --------------------------------------------------

func (s *store) ListBuckets(ctx context.Context) ([]string, error) {
	return s.client(s.cfg.PathStyle).ListBuckets(ctx)
}

func (s *store) WalkObjects(ctx context.Context, bucket, prefix, startAfter string, fn func(objstore.ObjectMeta) error) error {
	return s.clientFor(bucket).WalkObjects(ctx, bucket, prefix, startAfter, fn)
}

func (s *store) ListDirectory(ctx context.Context, bucket, prefix string, limit int) ([]string, []objstore.ObjectMeta, bool, error) {
	return s.clientFor(bucket).ListDirectory(ctx, bucket, prefix, limit)
}

func (s *store) ListPage(ctx context.Context, bucket, prefix, cursor string, limit int) (objstore.PageResult, error) {
	return s.clientFor(bucket).ListPage(ctx, bucket, prefix, cursor, limit)
}

func (s *store) ListBucketsPage(ctx context.Context, cursor string, limit int) (names []string, nextCursor string, hasMore bool, err error) {
	return s.client(s.cfg.PathStyle).ListBucketsPage(ctx, cursor, limit)
}

func (s *store) NewSource(bucket, key string, size int64) format.Source {
	return s.clientFor(bucket).NewSource(bucket, key, size)
}

func (s *store) Catalog(bucket string) format.Catalog {
	return s.clientFor(bucket).Catalog(bucket)
}

func (s *store) Endpoint() string { return s.client(s.cfg.PathStyle).Endpoint() }

func (s *store) Region() string { return s.client(s.cfg.PathStyle).Region() }

func (s *store) BucketRegion(ctx context.Context, bucket string) (string, error) {
	return s.clientFor(bucket).BucketRegion(ctx, bucket)
}

// ---- objstore.QueryConfigurer ----------------------------------------------

// PreviewConfig computes the DuckDB preview parameters for this connection,
// applying AWS-specific normalization (virtual-hosted addressing, regional
// endpoints, instance-role credentials).
func (s *store) PreviewConfig() objstore.PreviewConfig {
	endpoint := s.cfg.Endpoint
	useSSL := s.cfg.UseSSL
	region := s.cfg.Region
	useRole := s.cfg.UseRole
	urlStyle := "path"
	if s.aws {
		urlStyle = "vhost"
		useSSL = true
		if region == "" {
			region = "us-east-1"
		}
		if s.cfg.AccessKey == "" {
			useRole = true
		}
		// For real AWS, the DuckDB secret should not pin a custom endpoint — let
		// it use AWS's regional endpoints.
		endpoint = ""
	}
	if s.cfg.PathStyle {
		urlStyle = "path"
	}
	return objstore.PreviewConfig{
		Endpoint: endpoint, Region: region, AccessKey: s.cfg.AccessKey,
		SecretKey: s.cfg.SecretKey, SessionToken: s.cfg.SessionToken,
		UseSSL: useSSL, URLStyle: urlStyle, UseRole: useRole,
	}
}
