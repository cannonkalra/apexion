package crawler

import (
	"sort"
	"strings"

	"github.com/apexion/apexion/internal/crawler/s3"
	"github.com/apexion/apexion/internal/model"
)

// aggregate accumulates per-dataset state during the object walk. Its memory is
// bounded by the number of datasets and partitions, not the number of objects.
type aggregate struct {
	datasets map[string]*dsAgg
	tables   map[string]model.Format
}

func newAggregate() *aggregate {
	return &aggregate{datasets: map[string]*dsAgg{}, tables: map[string]model.Format{}}
}

type repObject struct {
	key  string
	size int64
}

type partAgg struct {
	values map[string]string
	count  int64
	size   int64
}

type dsAgg struct {
	root        string
	tableFormat model.Format
	formatVotes map[model.Format]int
	fileCount   int64
	totalSize   int64
	rowEstimate int64
	partKeySet  map[string]bool
	partKeys    []string
	partitions  map[string]*partAgg
	reps        map[model.Format]repObject
	changed     bool
}

func newDsAgg(root string) *dsAgg {
	return &dsAgg{
		root:        root,
		formatVotes: map[model.Format]int{},
		partKeySet:  map[string]bool{},
		partitions:  map[string]*partAgg{},
		reps:        map[model.Format]repObject{},
	}
}

func (a *aggregate) get(root string) *dsAgg {
	d, ok := a.datasets[root]
	if !ok {
		d = newDsAgg(root)
		a.datasets[root] = d
	}
	return d
}

func (a *aggregate) markTable(root string, f model.Format) {
	a.tables[root] = f
	d := a.get(root)
	d.tableFormat = f
}

func (a *aggregate) add(pi partitionInfo, f model.Format, om s3.ObjectMeta, changed bool) {
	d := a.get(pi.root)
	d.formatVotes[f]++
	d.fileCount++
	d.totalSize += om.Size
	if changed {
		d.changed = true
	}
	for _, k := range pi.keys {
		if !d.partKeySet[k] {
			d.partKeySet[k] = true
			d.partKeys = append(d.partKeys, k)
		}
	}
	if len(pi.keys) > 0 {
		combo := partitionCombo(pi)
		p, ok := d.partitions[combo]
		if !ok {
			p = &partAgg{values: pi.values}
			d.partitions[combo] = p
		}
		p.count++
		p.size += om.Size
	}
	if r, ok := d.reps[f]; !ok || om.Size >= r.size {
		d.reps[f] = repObject{key: om.Key, size: om.Size}
	}
}

// finalDatasets folds file-based datasets that live under a table-format root
// into that table dataset, and returns the datasets to catalog.
func (a *aggregate) finalDatasets() []*dsAgg {
	tableRoots := make([]string, 0, len(a.tables))
	for r := range a.tables {
		tableRoots = append(tableRoots, r)
	}
	// Longest roots first so nested tables win.
	sort.Slice(tableRoots, func(i, j int) bool { return len(tableRoots[i]) > len(tableRoots[j]) })

	var out []*dsAgg
	for root, d := range a.datasets {
		if d.tableFormat != "" {
			out = append(out, d)
			continue
		}
		if owner := coveringTable(root, tableRoots); owner != "" {
			od := a.datasets[owner]
			od.fileCount += d.fileCount
			od.totalSize += d.totalSize
			for combo, p := range d.partitions {
				if _, ok := od.partitions[combo]; !ok {
					od.partitions[combo] = p
				}
			}
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].root < out[j].root })
	return out
}

func coveringTable(root string, tableRoots []string) string {
	for _, tr := range tableRoots {
		if root == tr || strings.HasPrefix(root, tr+"/") {
			return tr
		}
	}
	return ""
}

func partitionCombo(pi partitionInfo) string {
	parts := make([]string, 0, len(pi.keys))
	for _, k := range pi.keys {
		parts = append(parts, k+"="+pi.values[k])
	}
	return strings.Join(parts, "/")
}

func (d *dsAgg) resolvedFormat() model.Format {
	if d.tableFormat != "" {
		return d.tableFormat
	}
	return dominantFormat(d.formatVotes)
}

func (d *dsAgg) name(bucket string) string { return datasetName(bucket, d.root) }

func (d *dsAgg) partitionSlice() []*partAgg {
	out := make([]*partAgg, 0, len(d.partitions))
	for _, p := range d.partitions {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return partKey(out[i]) < partKey(out[j]) })
	return out
}

func partKey(p *partAgg) string {
	keys := make([]string, 0, len(p.values))
	for k := range p.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(p.values[k])
		b.WriteByte('/')
	}
	return b.String()
}
