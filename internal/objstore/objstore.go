// Package objstore is the storage abstraction the application depends on
// instead of any concrete SDK. Explorer, crawler, catalog and connections work
// only against these interfaces; only a provider package (the s3 provider being
// the first) imports a storage SDK and implements them.
//
// The interfaces are deliberately small and composed rather than one god
// interface, so a consumer can depend on just the capability it needs and a
// future provider can implement them incrementally.
//
// Adding a new backend (LocalFS, Azure, GCS, ...) requires only: a package that
// implements ObjectStore + Connector, a features.RegisterStorageProvider call
// from its init(), and compiling it in. Nothing elsewhere changes.
//
// Deliberately does NOT: know anything about the domains (explorer/crawler/
// catalog), the UI, HTTP, or DuckDB. Providers implement these contracts only.
package objstore

import (
	"context"
	"time"

	"github.com/apexion/apexion/internal/format"
)

// ObjectMeta is a lightweight listing record — the storage-neutral view of one
// object, replacing the old provider-specific listing type.
type ObjectMeta struct {
	Key          string
	ETag         string
	Size         int64
	LastModified time.Time
	StorageClass string
	VersionID    string
}

// bucketLister lists the buckets/containers a connection can see.
type bucketLister interface {
	ListBuckets(ctx context.Context) ([]string, error)
}

// walker streams every object under a prefix (recursive, paginated) to fn.
// Returning an error from fn stops the walk.
type walker interface {
	WalkObjects(ctx context.Context, bucket, prefix, startAfter string, fn func(ObjectMeta) error) error
}

// dirLister lists the immediate children of a prefix like a file browser:
// sub-folders and files, non-recursive, bounded by limit.
type dirLister interface {
	ListDirectory(ctx context.Context, bucket, prefix string, limit int) (folders []string, files []ObjectMeta, truncated bool, err error)
}

// PageResult is one bounded, cursor-resumable page of a directory-style
// (delimiter) listing: immediate sub-folders and files under a prefix.
type PageResult struct {
	Folders    []string     // common prefixes, each incl. trailing slash
	Files      []ObjectMeta // immediate files (non-recursive)
	NextCursor string       // opaque; feed back to resume. "" when !HasMore
	HasMore    bool         // more children exist beyond this page
}

// pager lists immediate children of a prefix one bounded page at a time,
// resuming from an opaque cursor. cursor=="" starts at the beginning.
// limit<=0 means provider default. The cursor is provider-defined and opaque
// to callers; only pass back a NextCursor this same provider returned.
type pager interface {
	ListPage(ctx context.Context, bucket, prefix, cursor string, limit int) (PageResult, error)
}

// bucketPager lists buckets one bounded page at a time (synthetic paging is
// allowed where the SDK returns all buckets at once).
type bucketPager interface {
	ListBucketsPage(ctx context.Context, cursor string, limit int) (names []string, nextCursor string, hasMore bool, err error)
}

// objectOpener adapts individual objects to the format layer's IO abstractions,
// so readers and table-format resolvers stay storage-agnostic.
type objectOpener interface {
	// NewSource returns a streaming + random-access view of one object.
	NewSource(bucket, key string, size int64) format.Source
	// Catalog returns directory-style metadata access rooted at a bucket, used
	// by the table-format resolvers.
	Catalog(bucket string) format.Catalog
}

// describer exposes connection-level metadata and per-bucket region priming.
type describer interface {
	Endpoint() string
	Region() string
	BucketRegion(ctx context.Context, bucket string) (string, error)
}

// ObjectStore is the full capability set a connected backend exposes. It is the
// composition of the narrow interfaces above; consumers should accept the
// smallest one they actually use.
type ObjectStore interface {
	bucketLister
	walker
	dirLister
	pager
	bucketPager
	objectOpener
	describer
}

// Provider hands out the ObjectStore for the currently-active connection, so
// long-lived services follow connection switches without being rebuilt.
// StoreFor may return a variant whose addressing is tuned to the given bucket.
type Provider interface {
	Store() ObjectStore
	StoreFor(bucket string) ObjectStore
}

// Config carries the connection parameters a Connector needs. The fields are
// storage-neutral connection settings; a provider interprets the ones that
// apply to it (e.g. PathStyle is meaningful to S3, ignored by others). Provider
// is the selected provider id/alias so a provider can apply subtype-specific
// policy (e.g. the s3 provider treats "aws" specially) without the caller
// knowing anything about it.
type Config struct {
	Provider  string
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Region    string
	UseRole   bool
	PathStyle bool
}

// Connector builds a connected ObjectStore from a Config. Each provider
// registers one with the feature registry so connections can construct clients
// without importing the provider's SDK.
type Connector interface {
	Connect(Config) (ObjectStore, error)
}

// PreviewConfig is the storage-access configuration the DuckDB preview engine
// needs to read a backend's objects. The provider computes it (applying any
// backend-specific normalization); the preview engine just applies it. All
// fields are pre-normalized — the consumer performs no provider-specific logic.
type PreviewConfig struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	UseSSL    bool
	URLStyle  string // "path" or "vhost"
	UseRole   bool
}

// QueryConfigurer is implemented by stores that can describe themselves to the
// DuckDB-backed preview/query engine. The active connection's store provides
// this; the engine reconfigures itself from it on connection switches.
type QueryConfigurer interface {
	PreviewConfig() PreviewConfig
}
