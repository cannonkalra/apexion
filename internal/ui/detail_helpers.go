package ui

import (
	"fmt"
	"strings"

	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/model"
)

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func columnCount(d *catalog.DatasetDetail) int {
	if d.Schema == nil {
		return 0
	}
	return len(d.Schema.Columns)
}

func schemaSubtitle(d *catalog.DatasetDetail) string {
	if d.Schema == nil {
		return "not yet inferred"
	}
	return fmt.Sprintf("v%d · %d columns", d.Schema.Version, len(d.Schema.Columns))
}

func statDistinct(c model.Column) string {
	if c.Statistics == nil {
		return "—"
	}
	return humanCount(c.Statistics.DistinctCount)
}

func statComplete(c model.Column) string {
	if c.Statistics == nil {
		return "—"
	}
	return pct(c.Statistics.Completeness)
}

func statSamples(c model.Column) string {
	if c.Statistics == nil || len(c.Statistics.SampleValues) == 0 {
		return "—"
	}
	vals := c.Statistics.SampleValues
	if len(vals) > 3 {
		vals = vals[:3]
	}
	return truncate(strings.Join(vals, ", "), 40)
}

func partitionKeysLabel(d *catalog.DatasetDetail) string {
	if len(d.Dataset.PartitionKeys) == 0 {
		return "None"
	}
	return strings.Join(d.Dataset.PartitionKeys, ", ")
}

func schemaVersionLabel(d *catalog.DatasetDetail) string {
	if d.Schema == nil {
		return "—"
	}
	return fmt.Sprintf("v%d", d.Schema.Version)
}

func previewURLForObject(o model.Object) string {
	v := urlValues(o.BucketName, o.Key, string(o.Format))
	return "/preview?" + v
}

func sampleHead(rows []map[string]string, n int) []map[string]string {
	if len(rows) > n {
		return rows[:n]
	}
	return rows
}

func partitionHead(parts []model.Partition, n int) []model.Partition {
	if len(parts) > n {
		return parts[:n]
	}
	return parts
}
