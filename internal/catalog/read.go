package catalog

import (
	"context"
	"encoding/json"

	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// DatasetDetail is the composite read model for the dataset page.
type DatasetDetail struct {
	Dataset       model.Dataset          `json:"dataset"`
	Schema        *model.Schema          `json:"schema"`
	Partitions    []model.Partition      `json:"partitions"`
	SampleColumns []string               `json:"sample_columns"`
	SampleRows    []map[string]string    `json:"sample_rows"`
	Inference     *model.InferenceResult `json:"inference"`
	InferenceRun  *model.InferenceRun    `json:"inference_run"`
	Objects       []model.Object         `json:"objects"`
	InferenceRuns []model.InferenceRun   `json:"inference_runs"`
	CrawlRuns     []model.CrawlerRun     `json:"crawl_runs"`
}

// DatasetDetail assembles everything the dataset page needs.
func (s *Service) DatasetDetail(ctx context.Context, id string) (*DatasetDetail, error) {
	ds, err := s.store.GetDataset(ctx, id)
	if err != nil || ds == nil {
		return nil, err
	}
	d := &DatasetDetail{Dataset: *ds}

	if d.Schema, err = s.store.LatestSchema(ctx, id); err != nil {
		return nil, err
	}
	if d.Partitions, err = s.store.ListPartitions(ctx, id); err != nil {
		return nil, err
	}
	if d.Objects, err = s.store.ListObjects(ctx, ds.BucketID, id, 50); err != nil {
		return nil, err
	}

	if sample, _ := s.store.GetSample(ctx, id); sample != nil {
		var objs []map[string]string
		if json.Unmarshal([]byte(sample.RowsJSON), &objs) == nil {
			d.SampleRows = objs
		}
		if d.Schema != nil {
			for _, c := range d.Schema.Columns {
				d.SampleColumns = append(d.SampleColumns, c.Name)
			}
		} else if len(objs) > 0 {
			for k := range objs[0] {
				d.SampleColumns = append(d.SampleColumns, k)
			}
		}
	}

	if run, _ := s.store.LatestInferenceRun(ctx, id); run != nil {
		d.InferenceRun = run
		if run.Status == model.StatusCompleted {
			var res model.InferenceResult
			if json.Unmarshal([]byte(run.Findings), &res) == nil {
				d.Inference = &res
			}
		}
	}
	d.InferenceRuns, _ = s.store.ListInferenceRuns(ctx, id, 20)
	d.CrawlRuns, _ = s.store.ListCrawlerRuns(ctx, ds.BucketID, 10)
	return d, nil
}

// ListDatasets is a thin pass-through used by the UI/API.
func (s *Service) ListDatasets(ctx context.Context, f storage.DatasetFilter) ([]model.Dataset, error) {
	return s.store.ListDatasets(ctx, f)
}
