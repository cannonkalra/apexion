// Package parquet implements a schema reader for Apache Parquet files. It reads
// the footer via random access (no full download) to recover the Arrow-style
// schema, logical types, and an exact row count, then samples a few rows.
package parquet

import (
	"context"
	"fmt"
	"io"
	"strings"

	pq "github.com/parquet-go/parquet-go"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// Reader reads Parquet schema + sample.
type Reader struct{}

// New returns a Parquet reader.
func New() *Reader { return &Reader{} }

func (r *Reader) Format() model.Format { return model.FormatParquet }

func (r *Reader) ReadSchema(ctx context.Context, src format.Source, opts format.Options) (*format.Result, error) {
	ra, size, err := src.ReaderAt(ctx)
	if err != nil {
		return nil, fmt.Errorf("reader-at: %w", err)
	}
	f, err := pq.OpenFile(ra, size)
	if err != nil {
		return nil, fmt.Errorf("open parquet: %w", err)
	}

	schema := f.Schema()
	topFields := schema.Fields()
	fields := make([]format.Field, len(topFields))
	for i, fld := range topFields {
		fields[i] = format.Field{
			Name:         fld.Name(),
			Type:         mapParquetType(fld),
			PhysicalType: physicalName(fld),
			Nullable:     fld.Optional(),
		}
	}

	result := &format.Result{
		Format:           model.FormatParquet,
		Compression:      mapParquetCompression(f),
		Fields:           fields,
		RowCountEstimate: f.NumRows(),
	}

	// Sample rows using the high-level reader into maps.
	maxRows := opts.SampleRows
	if maxRows <= 0 {
		maxRows = 1000
	}
	if maxRows > 2000 {
		maxRows = 2000
	}
	result.Rows = sampleRows(ra, fields, maxRows)
	return result, nil
}

func sampleRows(ra io.ReaderAt, fields []format.Field, maxRows int) [][]string {
	reader := pq.NewReader(ra)
	defer reader.Close()
	var rows [][]string
	for len(rows) < maxRows {
		row := map[string]any{}
		if err := reader.Read(&row); err != nil {
			break
		}
		cells := make([]string, len(fields))
		for i, fld := range fields {
			cells[i] = renderValue(row[fld.Name])
		}
		rows = append(rows, cells)
	}
	return rows
}

func renderValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case map[string]any, []any:
		return fmt.Sprintf("%v", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// mapParquetType maps a parquet node to a logical Apexion type, preferring the
// logical/converted type annotation over the physical kind.
func mapParquetType(node pq.Node) model.DataType {
	if !node.Leaf() {
		if node.Repeated() {
			return model.TypeArray
		}
		if len(node.Fields()) > 0 {
			// Maps are encoded as repeated key_value groups.
			for _, sub := range node.Fields() {
				if sub.Name() == "key_value" || sub.Name() == "map" {
					return model.TypeMap
				}
			}
			return model.TypeStruct
		}
	}
	t := node.Type()
	if lt := t.LogicalType(); lt != nil {
		switch {
		case lt.UUID != nil:
			return model.TypeUUID
		case lt.Json != nil, lt.Bson != nil:
			return model.TypeJSON
		case lt.Date != nil:
			return model.TypeDate
		case lt.Time != nil:
			return model.TypeTime
		case lt.Timestamp != nil:
			return model.TypeTimestamp
		case lt.Decimal != nil:
			return model.TypeDecimal
		case lt.Integer != nil:
			return model.TypeInteger
		case lt.UTF8 != nil, lt.Enum != nil:
			return model.TypeString
		case lt.List != nil:
			return model.TypeArray
		case lt.Map != nil:
			return model.TypeMap
		}
	}
	switch t.Kind() {
	case pq.Boolean:
		return model.TypeBoolean
	case pq.Int32, pq.Int64, pq.Int96:
		return model.TypeInteger
	case pq.Float, pq.Double:
		return model.TypeFloat
	case pq.ByteArray, pq.FixedLenByteArray:
		return model.TypeString
	default:
		return model.TypeString
	}
}

func physicalName(node pq.Node) string {
	if !node.Leaf() {
		if node.Repeated() {
			return "repeated"
		}
		return "group"
	}
	return node.Type().Kind().String()
}

func mapParquetCompression(f *pq.File) model.Compression {
	// Parquet compression is internal and per-column; we report the codec of
	// the first column chunk as a representative value.
	if len(f.Metadata().RowGroups) > 0 && len(f.Metadata().RowGroups[0].Columns) > 0 {
		codec := f.Metadata().RowGroups[0].Columns[0].MetaData.Codec
		return codecName(codec.String())
	}
	return model.CompressionNone
}

func codecName(s string) model.Compression {
	switch strings.ToUpper(s) {
	case "SNAPPY":
		return model.CompressionSnappy
	case "GZIP":
		return model.CompressionGzip
	case "ZSTD":
		return model.CompressionZstd
	case "LZ4", "LZ4_RAW":
		return model.CompressionLZ4
	case "BROTLI":
		return model.CompressionBrotli
	default:
		return model.CompressionNone
	}
}
