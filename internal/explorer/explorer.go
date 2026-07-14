// Package explorer provides a VS Code-style browse experience over object
// storage: lazy directory listings, folder summaries, and file metadata. It
// reads live from S3 (no catalog dependency), so it works before anything is
// crawled.
package explorer

import (
	"context"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/apexion/apexion/internal/crawler/format"
	"github.com/apexion/apexion/internal/crawler/s3"
	"github.com/apexion/apexion/internal/model"
)

// Service browses object storage using the active connection.
type Service struct {
	provider s3.Provider
}

// New creates an explorer service backed by a connection provider.
func New(provider s3.Provider) *Service { return &Service{provider: provider} }

func (s *Service) clientFor(bucket string) *s3.Client { return s.provider.ClientFor(bucket) }

// FolderEntry is a sub-directory in a listing.
type FolderEntry struct {
	Name string // display name (last path segment)
	Path string // full prefix incl. trailing slash
}

// FileEntry is a file in a listing.
type FileEntry struct {
	Name        string
	Key         string
	Size        int64
	Modified    time.Time
	Format      model.Format
	Compression model.Compression
}

// FolderSummary aggregates a prefix (bounded scan).
type FolderSummary struct {
	Files        int64
	Size         int64
	Formats      []model.Format
	LastModified time.Time
	Truncated    bool
}

// DirListing is the immediate contents of a prefix plus a summary.
type DirListing struct {
	Bucket  string
	Prefix  string
	Folders []FolderEntry
	Files   []FileEntry
	Summary FolderSummary
}

// ListDir returns the immediate children of a prefix and a bounded summary of
// everything beneath it.
func (s *Service) ListDir(ctx context.Context, bucket, prefix string) (*DirListing, error) {
	folders, files, err := s.clientFor(bucket).ListDirectory(ctx, bucket, prefix)
	if err != nil {
		return nil, err
	}
	out := &DirListing{Bucket: bucket, Prefix: prefix}
	for _, f := range folders {
		out.Folders = append(out.Folders, FolderEntry{Name: folderName(f), Path: f})
	}
	for _, om := range files {
		out.Files = append(out.Files, FileEntry{
			Name:        path.Base(om.Key),
			Key:         om.Key,
			Size:        om.Size,
			Modified:    om.LastModified,
			Format:      format.DetectFormat(om.Key, nil),
			Compression: format.DetectCompression(om.Key),
		})
	}
	sort.Slice(out.Folders, func(i, j int) bool { return out.Folders[i].Name < out.Folders[j].Name })
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Name < out.Files[j].Name })

	summary, err := s.FolderSummary(ctx, bucket, prefix)
	if err == nil {
		out.Summary = *summary
	}
	return out, nil
}

// FolderSummary walks up to a bounded number of objects under a prefix to
// compute file count, size, formats, and last-modified.
func (s *Service) FolderSummary(ctx context.Context, bucket, prefix string) (*FolderSummary, error) {
	const cap = 20000
	sum := &FolderSummary{}
	formats := map[model.Format]bool{}
	err := s.clientFor(bucket).WalkObjects(ctx, bucket, prefix, "", func(om s3.ObjectMeta) error {
		if strings.HasSuffix(om.Key, "/") {
			return nil
		}
		sum.Files++
		sum.Size += om.Size
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

// Breadcrumb splits a prefix into cumulative path segments for navigation.
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
