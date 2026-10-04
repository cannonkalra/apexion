package ui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/pricing"
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

// pageSizeOptions returns the per-page dropdown options with the current size
// selected, so the user controls how many items each explorer page loads.
func pageSizeOptions(current int) []sortOption {
	if current <= 0 {
		current = explorer.DefaultPageSize
	}
	opts := make([]sortOption, 0, len(explorer.PageSizeOptions))
	for _, n := range explorer.PageSizeOptions {
		opts = append(opts, sortOption{
			Value:    strconv.Itoa(n),
			Label:    strconv.Itoa(n) + " / page",
			Selected: n == current,
		})
	}
	return opts
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

// costLabel formats an estimated monthly storage cost in USD: cents for
// anything from $0.01, "<$0.01" for a non-zero smaller amount. truncated marks
// a folder whose bounded scan stopped early, so the cost is a lower bound.
func costLabel(usd float64, truncated bool) string {
	var s string
	switch {
	case usd == 0:
		s = "$0.00"
	case usd < 0.01:
		s = "<$0.01"
	default:
		s = "$" + thousands(fmt.Sprintf("%.2f", usd))
	}
	if truncated {
		return "≥ " + s
	}
	return s
}

// thousands inserts commas into the integer part of a decimal string.
func thousands(s string) string {
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if frac != "" {
		b.WriteString("." + frac)
	}
	return b.String()
}

// costBasis explains a cost figure: storage only, at AWS list prices for the
// pricing region (non-AWS stores are shown as their S3 equivalent).
func costBasis(region string) string {
	return "Estimated monthly S3 storage cost at AWS list prices for " + region +
		" (storage only; first volume tier; prices as of " + pricingDate() + ")"
}

// fileCostTitle is the tooltip for one file's cost: its class and exact amount.
func fileCostTitle(region string, f explorer.FileEntry) string {
	class := f.StorageClass
	if class == "" {
		class = pricing.Standard
	}
	return fmt.Sprintf("%s/month · %s · $%g per GB-month\n%s",
		exactUSD(f.Cost), class, pricing.Rate(region, class), costBasis(region))
}

// folderCostTitle is the tooltip for a folder's cost.
func folderCostTitle(s explorer.FolderSummary) string {
	t := fmt.Sprintf("%s/month for %s files\n%s", exactUSD(s.Cost), humanCount(s.Files), costBasis(s.PriceRegion))
	if s.Truncated {
		t += "\nOnly the first " + humanCount(s.Files) + " files were scanned, so the folder costs at least this."
	}
	return t
}

// exactUSD shows a cost precisely enough to be meaningful for tiny objects:
// cents from $0.01, otherwise three significant digits ($0.0000000214).
func exactUSD(usd float64) string {
	if usd == 0 || usd >= 0.01 {
		return "$" + thousands(fmt.Sprintf("%.2f", usd))
	}
	v, _ := strconv.ParseFloat(fmt.Sprintf("%.3g", usd), 64)
	return "$" + strconv.FormatFloat(v, 'f', -1, 64)
}

// pricingDate is the date of the AWS price list the rates were generated from.
func pricingDate() string {
	d, _, _ := strings.Cut(pricing.RatesPublished, "T")
	return d
}
