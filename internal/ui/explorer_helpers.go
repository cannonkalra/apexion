package ui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"time"

	"github.com/apexion/apexion/internal/explorer"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/preview"
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
	from, err := preview.FromClause(vm.Bucket, vm.Key, vm.Format)
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
