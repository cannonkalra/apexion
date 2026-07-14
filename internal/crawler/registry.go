package crawler

import (
	"github.com/apexion/apexion/internal/crawler/avro"
	"github.com/apexion/apexion/internal/crawler/csv"
	"github.com/apexion/apexion/internal/crawler/delta"
	"github.com/apexion/apexion/internal/crawler/format"
	"github.com/apexion/apexion/internal/crawler/iceberg"
	jsonreader "github.com/apexion/apexion/internal/crawler/json"
	"github.com/apexion/apexion/internal/crawler/orc"
	"github.com/apexion/apexion/internal/crawler/parquet"
	"github.com/apexion/apexion/internal/model"
)

// DefaultRegistry builds the format reader registry with every built-in
// file-format reader registered. New formats plug in here.
func DefaultRegistry() *format.Registry {
	r := format.NewRegistry()
	r.Register(csv.NewCSV())
	r.Register(csv.NewTSV())
	r.Register(jsonreader.NewJSON())
	r.Register(jsonreader.NewJSONL())
	r.Register(parquet.New())
	r.Register(avro.New())
	r.Register(orc.New())
	return r
}

// DefaultResolvers builds the table-format resolvers (dataset-level plugins).
func DefaultResolvers() map[model.Format]format.TableResolver {
	return map[model.Format]format.TableResolver{
		model.FormatIceberg: iceberg.New(),
		model.FormatDelta:   delta.New(),
	}
}
