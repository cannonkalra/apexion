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

// loadMoreURL bumps the page size to reveal more of a large folder, preserving
// the current search and sort.
func loadMoreURL(l *explorer.DirListing) string {
	next := l.Limit * 4
	if next <= 0 {
		next = explorer.DefaultPageSize * 4
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
	v.Set("limit", strconv.Itoa(next))
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
