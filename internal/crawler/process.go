package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/apexion/apexion/internal/crawler/format"
	"github.com/apexion/apexion/internal/events"
	"github.com/apexion/apexion/internal/model"
)

// processOne resolves and persists the schema, sample, partitions, and object
// associations for a single dataset.
func (c *Crawler) processOne(ctx context.Context, bucket *model.Bucket, d *dsAgg) error {
	f := d.resolvedFormat()
	result, err := c.resolveSchema(ctx, bucket, d, f)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	partKeys := unionStrings(d.partKeys, result.PartitionKeys)

	// Reuse an existing dataset record (preserve id + created_at).
	existing, _ := c.store.GetDatasetByPath(ctx, bucket.ID, d.root)
	ds := &model.Dataset{
		ID:            uuid.NewString(),
		BucketID:      bucket.ID,
		BucketName:    bucket.Name,
		Name:          d.name(bucket.Name),
		Path:          d.root,
		Format:        f,
		Compression:   result.Compression,
		FileCount:     d.fileCount,
		TotalSize:     d.totalSize,
		RowCount:      estimateDatasetRows(result, d.fileCount),
		PartitionKeys: partKeys,
		CreatedAt:     now,
		UpdatedAt:     now,
		LastScanAt:    &now,
	}
	if existing != nil {
		ds.ID = existing.ID
		ds.CreatedAt = existing.CreatedAt
		ds.Description = existing.Description
		ds.SchemaID = existing.SchemaID
	}

	// Schema versioning: only write a new version when the shape changes.
	if len(result.Fields) > 0 {
		fp := fingerprint(result.Fields)
		latest, err := c.store.LatestSchema(ctx, ds.ID)
		if err != nil {
			return err
		}
		if latest == nil || latest.Fingerprint != fp {
			version := 1
			if latest != nil {
				version = latest.Version + 1
			}
			schema := buildSchema(ds.ID, version, fp, result, now)
			if err := c.store.SaveSchema(ctx, schema); err != nil {
				return err
			}
			ds.SchemaID = schema.ID
			old := 0
			if latest != nil {
				old = latest.Version
			}
			c.emit(events.TypeSchemaChanged, ds.ID, events.SchemaChangedData{
				DatasetID: ds.ID, SchemaID: schema.ID, Version: version,
				OldVersion: old, Columns: len(schema.Columns),
			})
		}
	}

	if err := c.store.UpsertDataset(ctx, ds); err != nil {
		return err
	}

	// Persist the sample rows for the UI and inference.
	if len(result.Rows) > 0 {
		if err := c.saveSample(ctx, ds.ID, result, now); err != nil {
			c.log.Warn().Err(err).Msg("save sample")
		}
	}

	// Partitions.
	if parts := buildPartitions(ds.ID, d, now); len(parts) > 0 {
		if err := c.store.ReplacePartitions(ctx, ds.ID, parts); err != nil {
			c.log.Warn().Err(err).Msg("replace partitions")
		}
		for _, p := range parts {
			c.emit(events.TypeNewPartition, ds.ID, events.NewPartitionData{
				DatasetID: ds.ID, PartitionID: p.ID, Path: p.Path, Values: p.Values,
			})
		}
	}

	// Associate every object under the root with this dataset.
	if err := c.store.AssignObjectsByPrefix(ctx, bucket.ID, ds.ID, d.root); err != nil {
		c.log.Warn().Err(err).Msg("assign objects")
	}

	c.emit(events.TypeDatasetDiscovered, ds.ID, events.DatasetDiscoveredData{
		DatasetID: ds.ID, Name: ds.Name, Bucket: bucket.Name, Path: ds.Path,
		Format: string(f), FileCount: ds.FileCount,
	})
	c.emit(events.TypeCatalogUpdated, ds.ID, events.CatalogUpdatedData{
		Entity: "dataset", RefID: ds.ID, Action: actionFor(existing),
	})
	return nil
}

// resolveSchema reads the schema either via a table resolver or a file reader.
func (c *Crawler) resolveSchema(ctx context.Context, bucket *model.Bucket, d *dsAgg, f model.Format) (*format.Result, error) {
	if resolver, ok := c.resolvers[f]; ok {
		res, ok, err := resolver.Detect(ctx, c.clientFor(bucket.Name).Catalog(bucket.Name), d.root)
		if err != nil {
			c.log.Warn().Err(err).Str("root", d.root).Msg("table resolver failed")
		}
		if ok && res != nil {
			return res, nil
		}
		return &format.Result{Format: f}, nil
	}

	reader := c.registry.Get(f)
	if reader == nil {
		return &format.Result{Format: f}, nil
	}
	rep, ok := d.reps[f]
	if !ok {
		return &format.Result{Format: f}, nil
	}
	src := c.clientFor(bucket.Name).NewSource(bucket.Name, rep.key, rep.size)
	opts := format.Options{
		SampleRows:  1000,
		SampleBytes: c.cfg.SampleBytes,
		Delimiter:   detectDelimiter(f),
		HasHeader:   true,
	}
	res, err := reader.ReadSchema(ctx, src, opts)
	if err != nil {
		c.log.Warn().Err(err).Str("key", rep.key).Str("format", string(f)).Msg("schema read failed")
		return &format.Result{Format: f}, nil
	}
	return res, nil
}

func (c *Crawler) saveSample(ctx context.Context, datasetID string, result *format.Result, now time.Time) error {
	maxRows := 200
	rows := result.Rows
	if len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	objs := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		m := make(map[string]string, len(result.Fields))
		for i, fld := range result.Fields {
			if i < len(r) {
				m[fld.Name] = r[i]
			}
		}
		objs = append(objs, m)
	}
	blob, _ := json.Marshal(objs)
	return c.store.SaveSample(ctx, &model.DataSample{
		ID: uuid.NewString(), DatasetID: datasetID, RowsJSON: string(blob),
		RowCount: len(objs), CreatedAt: now,
	})
}

func buildSchema(datasetID string, version int, fp string, result *format.Result, now time.Time) *model.Schema {
	schema := &model.Schema{
		ID: uuid.NewString(), DatasetID: datasetID, Version: version,
		Fingerprint: fp, CreatedAt: now,
	}
	for i, f := range result.Fields {
		col := model.Column{
			ID: uuid.NewString(), Name: f.Name, Position: i, DataType: f.Type,
			PhysicalType: f.PhysicalType, Nullable: f.Nullable,
			Statistics: columnStats(result.Rows, i),
		}
		schema.Columns = append(schema.Columns, col)
	}
	return schema
}

// columnStats computes lightweight profiling stats from sample rows.
func columnStats(rows [][]string, j int) *model.Statistics {
	if len(rows) == 0 {
		return nil
	}
	var nulls, count int64
	distinct := map[string]struct{}{}
	var samples []string
	var minV, maxV string
	first := true
	for _, r := range rows {
		if j >= len(r) {
			continue
		}
		count++
		v := r[j]
		if format.IsNull(v) {
			nulls++
			continue
		}
		if _, ok := distinct[v]; !ok {
			distinct[v] = struct{}{}
			if len(samples) < 10 {
				samples = append(samples, v)
			}
		}
		if first {
			minV, maxV, first = v, v, false
		} else {
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
	}
	st := &model.Statistics{
		ID: uuid.NewString(), NullCount: nulls, DistinctCount: int64(len(distinct)),
		MinValue: minV, MaxValue: maxV, SampleCount: count, SampleValues: samples,
	}
	nonNull := count - nulls
	if count > 0 {
		st.Completeness = float64(nonNull) / float64(count)
	}
	if nonNull > 0 {
		st.Uniqueness = float64(len(distinct)) / float64(nonNull)
	}
	return st
}

func buildPartitions(datasetID string, d *dsAgg, now time.Time) []model.Partition {
	var out []model.Partition
	for _, p := range d.partitionSlice() {
		out = append(out, model.Partition{
			ID: uuid.NewString(), DatasetID: datasetID, Path: partKey(p),
			Values: p.values, FileCount: p.count, Size: p.size, CreatedAt: now,
		})
	}
	return out
}

func estimateDatasetRows(result *format.Result, fileCount int64) int64 {
	if result.RowCountEstimate <= 0 {
		return 0
	}
	if fileCount <= 1 {
		return result.RowCountEstimate
	}
	return result.RowCountEstimate * fileCount
}

func detectDelimiter(f model.Format) rune {
	if f == model.FormatTSV {
		return '\t'
	}
	return ','
}

func unionStrings(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func actionFor(existing *model.Dataset) string {
	if existing == nil {
		return "created"
	}
	return "updated"
}

var _ = fmt.Sprintf
