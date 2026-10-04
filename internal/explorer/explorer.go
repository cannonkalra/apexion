// Package explorer provides a VS Code-style browse experience over object
// storage: lazy directory listings, folder summaries, and file metadata. It
// reads live through objstore (no catalog dependency), so it works before
// anything is crawled.
//
// Depends on: objstore, format, model.
// Deliberately does NOT: import ui/api/http; read or write the catalog database;
// or import a storage SDK (it only sees the objstore.Provider interface).
package explorer

import (
	"context"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/objstore"
	"github.com/apexion/apexion/internal/pricing"
)

// Service browses object storage using the active connection.
type Service struct {
	provider objstore.Provider
}

// New creates an explorer service backed by a connection provider.
func New(provider objstore.Provider) *Service { return &Service{provider: provider} }

func (s *Service) storeFor(bucket string) objstore.ObjectStore { return s.provider.StoreFor(bucket) }

// FolderEntry is a sub-directory in a listing.
type FolderEntry struct {
	Name string // display name (last path segment)
	Path string // full prefix incl. trailing slash
}

// FileEntry is a file in a listing.
type FileEntry struct {
	Name         string
	Key          string
	Size         int64
	Modified     time.Time
	Format       model.Format
	Compression  model.Compression
	StorageClass string
	Cost         float64 // estimated S3 storage cost, USD per month
}

// FolderSummary aggregates a prefix (bounded scan).
type FolderSummary struct {
	Files        int64
	Size         int64
	Formats      []model.Format
	LastModified time.Time
	Truncated    bool
	Cost         float64 // estimated S3 storage cost of the scanned files, USD per month
	PriceRegion  string  // region whose S3 list prices Cost uses
}

// DirListing is the immediate contents of a prefix (paginated). The folder
// summary is computed separately (lazily) — it is a recursive scan and would
// otherwise make every click slow on a large bucket.
type DirListing struct {
	Bucket     string
	Prefix     string
	Search     string // server-side name prefix filter within the folder
	Sort       string // one of the Sort* constants
	Folders    []FolderEntry
	Files      []FileEntry
	Limit      int
	Cursor     string // cursor that produced THIS page ("" = first page)
	NextCursor string // pass to the next ListDir call; "" when !HasMore
	HasMore    bool   // more children exist beyond this page
	// PriceRegion is the region whose S3 list prices the file costs use.
	PriceRegion string
}

// DefaultPageSize bounds how many immediate children a listing returns per page.
// Deliberately small so large folders paginate (Load More) instead of dumping
// everything at once; the UI offers larger page sizes via PageSizeOptions.
const DefaultPageSize = 100

// DefaultBucketPageSize bounds how many buckets a bucket listing returns per page.
const DefaultBucketPageSize = 50

// PageSizeOptions are the per-page choices the explorer UI offers for a listing.
var PageSizeOptions = []int{50, 100, 250, 500}

// Sort options for a listing.
const (
	SortNameAsc      = "name_asc"
	SortNameDesc     = "name_desc"
	SortSizeAsc      = "size_asc"
	SortSizeDesc     = "size_desc"
	SortModifiedAsc  = "modified_asc"
	SortModifiedDesc = "modified_desc"
)

// ListDir returns up to `limit` immediate children of a prefix, resumable from
// `cursor` (""=first page). `search` filters by name prefix server-side (S3
// Prefix), which also reduces the amount listed. `sortBy` orders the returned
// page. The recursive summary is computed separately (lazily).
func (s *Service) ListDir(ctx context.Context, bucket, prefix, search, sortBy, cursor string, limit int) (*DirListing, error) {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	// Server-side prefix filter: list keys beginning with prefix+search.
	pr, err := s.storeFor(bucket).ListPage(ctx, bucket, prefix+search, cursor, limit)
	if err != nil {
		return nil, err
	}
	region := s.priceRegion(ctx, bucket)
	out := &DirListing{
		Bucket: bucket, Prefix: prefix, Search: search, Sort: sortBy, Limit: limit,
		Cursor: cursor, NextCursor: pr.NextCursor, HasMore: pr.HasMore, PriceRegion: region,
	}
	for _, f := range pr.Folders {
		out.Folders = append(out.Folders, FolderEntry{Name: folderName(f), Path: f})
	}
	for _, om := range pr.Files {
		out.Files = append(out.Files, FileEntry{
			Name:         path.Base(om.Key),
			Key:          om.Key,
			Size:         om.Size,
			Modified:     om.LastModified,
			Format:       format.DetectFormat(om.Key, nil),
			Compression:  format.DetectCompression(om.Key),
			StorageClass: om.StorageClass,
			Cost:         pricing.ObjectCost(region, om.StorageClass, om.Size),
		})
	}
	sortListing(out, sortBy)
	return out, nil
}

// ListBucketsPage returns one bounded, cursor-resumable page of bucket names.
// Buckets are connection-level (not bucket-scoped), so it uses Store() rather
// than StoreFor.
func (s *Service) ListBucketsPage(ctx context.Context, cursor string, limit int) (names []string, nextCursor string, hasMore bool, err error) {
	if limit <= 0 {
		limit = DefaultBucketPageSize
	}
	return s.provider.Store().ListBucketsPage(ctx, cursor, limit)
}

// sortListing orders folders and files. Folders always sort by name (they carry
// no size/date); files sort by the requested field. Sorting applies to the
// fetched page — for a truncated listing, use a narrower search to sort the full
// set.
func sortListing(l *DirListing, sortBy string) {
	desc := strings.HasSuffix(sortBy, "_desc")
	sort.Slice(l.Folders, func(i, j int) bool {
		if desc {
			return l.Folders[i].Name > l.Folders[j].Name
		}
		return l.Folders[i].Name < l.Folders[j].Name
	})
	less := func(i, j int) bool { return l.Files[i].Name < l.Files[j].Name }
	switch sortBy {
	case SortNameDesc:
		less = func(i, j int) bool { return l.Files[i].Name > l.Files[j].Name }
	case SortSizeAsc:
		less = func(i, j int) bool { return l.Files[i].Size < l.Files[j].Size }
	case SortSizeDesc:
		less = func(i, j int) bool { return l.Files[i].Size > l.Files[j].Size }
	case SortModifiedAsc:
		less = func(i, j int) bool { return l.Files[i].Modified.Before(l.Files[j].Modified) }
	case SortModifiedDesc:
		less = func(i, j int) bool { return l.Files[i].Modified.After(l.Files[j].Modified) }
	}
	sort.Slice(l.Files, less)
}

// FolderSummary walks up to a bounded number of objects under a prefix to
// compute file count, size, formats, and last-modified. It is deliberately
// bounded (and loaded lazily by the UI) so it never blocks browsing.
func (s *Service) FolderSummary(ctx context.Context, bucket, prefix string) (*FolderSummary, error) {
	const cap = 5000
	sum := &FolderSummary{PriceRegion: s.priceRegion(ctx, bucket)}
	formats := map[model.Format]bool{}
	err := s.storeFor(bucket).WalkObjects(ctx, bucket, prefix, "", func(om objstore.ObjectMeta) error {
		if strings.HasSuffix(om.Key, "/") {
			return nil
		}
		sum.Files++
		sum.Size += om.Size
		sum.Cost += pricing.ObjectCost(sum.PriceRegion, om.StorageClass, om.Size)
		if om.LastModified.After(sum.LastModified) {
			sum.LastModified = om.LastModified
		}
		if f := format.DetectFormat(om.Key, nil); f != model.FormatUnknown {
			formats[f] = true
		}
		if sum.Files >= cap {
			sum.Truncated = true
			return errStop
		}
		return nil
	})
	if err != nil && err != errStop {
		return nil, err
	}
	for f := range formats {
		sum.Formats = append(sum.Formats, f)
	}
	sort.Slice(sum.Formats, func(i, j int) bool { return sum.Formats[i] < sum.Formats[j] })
	return sum, nil
}

// priceRegion is the region whose S3 list prices apply to a bucket: the
// bucket's own region when AWS publishes S3 rates for it, else
// pricing.DefaultRegion (non-AWS stores are priced as their S3 equivalent).
func (s *Service) priceRegion(ctx context.Context, bucket string) string {
	store := s.storeFor(bucket)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	region, err := store.BucketRegion(ctx, bucket)
	if err != nil || region == "" {
		region = store.Region()
	}
	return pricing.Region(region)
}

// Crumb is one cumulative path segment of a prefix, used for breadcrumb navigation.
type Crumb struct {
	Name string
	Path string
}

// Breadcrumbs builds navigational crumbs for a prefix.
func Breadcrumbs(prefix string) []Crumb {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return nil
	}
	segs := strings.Split(prefix, "/")
	crumbs := make([]Crumb, 0, len(segs))
	acc := ""
	for _, s := range segs {
		acc += s + "/"
		crumbs = append(crumbs, Crumb{Name: s, Path: acc})
	}
	return crumbs
}

func folderName(prefix string) string {
	p := strings.TrimRight(prefix, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// errStop is a sentinel used to stop a bounded walk early.
var errStop = stopError{}

type stopError struct{}

func (stopError) Error() string { return "stop" }
