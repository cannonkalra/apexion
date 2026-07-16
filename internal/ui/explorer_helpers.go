package ui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/model"
)

// parentExplorerURL links back to the folder containing a key.
func parentExplorerURL(bucket, key string) string {
	dir := path.Dir(key)
	if dir == "." {
		dir = ""
	} else {
		dir += "/"
	}
	v := url.Values{}
	v.Set("bucket", bucket)
	if dir != "" {
		v.Set("prefix", dir)
	}
	return "/explorer?" + v.Encode()
}

// sqlURLForFile pre-fills the scratchpad with a SELECT over the file.
func sqlURLForFile(vm PreviewVM) string {
	from, err := duckdb.FromClause(vm.Bucket, vm.Key, vm.Format)
	if err != nil {
		from = fmt.Sprintf("read_csv_auto('s3://%s/%s')", vm.Bucket, vm.Key)
	}
	q := fmt.Sprintf("SELECT * FROM %s LIMIT 100", from)
	v := url.Values{}
	v.Set("sql", q)
	return "/sql?" + v.Encode()
}

// jobElapsed formats how long a job ran (or has been running).
func jobElapsed(j model.Job) string {
	if j.StartedAt == nil {
		return "—"
	}
	end := time.Now()
	if j.FinishedAt != nil {
		end = *j.FinishedAt
	}
	d := end.Sub(*j.StartedAt)
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
}

// urlValues builds an encoded bucket/key/format query string.
func urlValues(bucket, key, format string) string {
	v := url.Values{}
	v.Set("bucket", bucket)
	v.Set("key", key)
	v.Set("format", format)
	return v.Encode()
}

// previewURL builds the file-preview page URL for a browsed file.
func previewURL(bucket string, f explorer.FileEntry) string {
	v := url.Values{}
	v.Set("bucket", bucket)
	v.Set("key", f.Key)
	v.Set("format", string(f.Format))
	return "/preview?" + v.Encode()
}

// crawlVals builds the hx-vals JSON payload for a Crawl Directory action.
func crawlVals(bucket, prefix string) string {
	b, _ := json.Marshal(map[string]string{"bucket": bucket, "prefix": prefix})
	return string(b)
}

func truncatedMark(truncated bool) string {
	if truncated {
		return "+"
	}
	return ""
}

// summaryQuery builds the query string for the lazy folder-summary partial.
func summaryQuery(bucket, prefix string) string {
	v := url.Values{}
	v.Set("bucket", bucket)
	if prefix != "" {
		v.Set("prefix", prefix)
	}
	return v.Encode()
}

// explorerPageURL builds the cursor-based load-more URL for the next page of the
// current folder. It targets the /ui/partials/explorer-page fragment route,
// carrying the same bucket/search/sort plus l.NextCursor so the server resumes
// exactly where this page ended. Unlike the old loadMoreURL, it does NOT grow
// the limit — each request fetches one bounded page and the rows are appended.
func explorerPageURL(l *explorer.DirListing, infinite bool) string {
	limit := l.Limit
	if limit <= 0 {
		limit = explorer.DefaultPageSize
	}
	v := url.Values{}
	v.Set("bucket", l.Bucket)
	if l.Prefix != "" {
		v.Set("prefix", l.Prefix)
	}
	if l.Search != "" {
		v.Set("search", l.Search)
	}
	if l.Sort != "" {
		v.Set("sort", l.Sort)
	}
	v.Set("cursor", l.NextCursor)
	v.Set("limit", strconv.Itoa(limit))
	if infinite {
		v.Set("infinite", "1")
	}
	return "/ui/partials/explorer-page?" + v.Encode()
}

// moreBucketsURL reveals the next slice of buckets by re-requesting /explorer
// with a larger bucket cap. The rendered rail is always capped server-side
// (see explorerPage), so the initial DOM never carries the full set; this link
// simply raises the cap by one page.
func moreBucketsURL(vm ExplorerVM) string {
	v := url.Values{}
	if vm.Bucket != "" {
		v.Set("bucket", vm.Bucket)
	}
	v.Set("buckets", strconv.Itoa(len(vm.Buckets)+explorer.DefaultBucketPageSize))
	return "/explorer?" + v.Encode()
}

type sortOption struct {
	Value    string
	Label    string
	Selected bool
}

// sortOptions returns the sort dropdown options with the current one selected.
func sortOptions(current string) []sortOption {
	if current == "" {
		current = explorer.SortNameAsc
	}
	opts := []sortOption{
		{explorer.SortNameAsc, "Name ↑", false},
		{explorer.SortNameDesc, "Name ↓", false},
		{explorer.SortSizeDesc, "Size ↓", false},
		{explorer.SortSizeAsc, "Size ↑", false},
		{explorer.SortModifiedDesc, "Modified ↓", false},
		{explorer.SortModifiedAsc, "Modified ↑", false},
	}
	for i := range opts {
		opts[i].Selected = opts[i].Value == current
	}
	return opts
}
