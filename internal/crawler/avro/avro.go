// Package avro implements a schema reader for Avro Object Container Files
// (OCF). It reads the file header schema (real, exact) and samples records.
package avro

import (
	"context"
	"fmt"
	"io"

	"github.com/hamba/avro/v2"
	"github.com/hamba/avro/v2/ocf"

	"github.com/apexion/apexion/internal/crawler/format"
	"github.com/apexion/apexion/internal/model"
)

// Reader reads Avro OCF schema + sample.
type Reader struct{}

// New returns an Avro reader.
func New() *Reader { return &Reader{} }

func (r *Reader) Format() model.Format { return model.FormatAvro }

func (r *Reader) ReadSchema(ctx context.Context, src format.Source, opts format.Options) (*format.Result, error) {
	rc, err := src.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer rc.Close()

	dec, err := ocf.NewDecoder(rc)
	if err != nil {
		return nil, fmt.Errorf("open avro ocf: %w", err)
	}

	schema := dec.Schema()
	rec, ok := schema.(*avro.RecordSchema)
	if !ok {
		return &format.Result{Format: model.FormatAvro}, nil
	}

	fields := make([]format.Field, 0, len(rec.Fields()))
	for _, f := range rec.Fields() {
		dt, nullable, phys := mapAvroSchema(f.Type())
		fields = append(fields, format.Field{
			Name:         f.Name(),
			Type:         dt,
			PhysicalType: phys,
			Nullable:     nullable,
		})
	}

	maxRows := opts.SampleRows
	if maxRows <= 0 {
		maxRows = 1000
	}
	if maxRows > 2000 {
		maxRows = 2000
	}
	var rows [][]string
	for dec.HasNext() && len(rows) < maxRows {
		m := map[string]any{}
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			break
		}
		cells := make([]string, len(fields))
		for i, fld := range fields {
			cells[i] = renderValue(m[fld.Name])
		}
		rows = append(rows, cells)
	}

	return &format.Result{
		Format:      model.FormatAvro,
		Compression: mapCodec(dec.Metadata()),
		Fields:      fields,
		Rows:        rows,
	}, nil
}

// mapAvroSchema returns the logical type, nullability, and physical type name
// for an Avro field schema, unwrapping nullable unions.
func mapAvroSchema(s avro.Schema) (model.DataType, bool, string) {
	nullable := false
	if u, ok := s.(*avro.UnionSchema); ok {
		nullable = u.Nullable()
		// Use the first non-null branch as the effective type.
		for _, t := range u.Types() {
			if t.Type() != avro.Null {
				s = t
				break
			}
		}
	}

	// Logical type overlays (uuid, date, timestamp, decimal).
	if lts, ok := s.(interface{ Logical() avro.LogicalSchema }); ok {
		if ls := lts.Logical(); ls != nil {
			switch ls.Type() {
			case avro.UUID:
				return model.TypeUUID, nullable, "uuid"
			case avro.Date:
				return model.TypeDate, nullable, "date"
			case avro.TimeMillis, avro.TimeMicros:
				return model.TypeTime, nullable, "time"
			case avro.TimestampMillis, avro.TimestampMicros,
				avro.LocalTimestampMillis, avro.LocalTimestampMicros:
				return model.TypeTimestamp, nullable, "timestamp"
			case avro.Decimal:
				return model.TypeDecimal, nullable, "decimal"
			}
		}
	}

	switch s.Type() {
	case avro.Boolean:
		return model.TypeBoolean, nullable, "boolean"
	case avro.Int, avro.Long:
		return model.TypeInteger, nullable, string(s.Type())
	case avro.Float, avro.Double:
		return model.TypeFloat, nullable, string(s.Type())
	case avro.String, avro.Enum:
		return model.TypeString, nullable, string(s.Type())
	case avro.Bytes, avro.Fixed:
		return model.TypeBinary, nullable, string(s.Type())
	case avro.Array:
		return model.TypeArray, nullable, "array"
	case avro.Map:
		return model.TypeMap, nullable, "map"
	case avro.Record:
		return model.TypeStruct, nullable, "record"
	default:
		return model.TypeString, nullable, string(s.Type())
	}
}

func mapCodec(meta map[string][]byte) model.Compression {
	if meta == nil {
		return model.CompressionNone
	}
	switch string(meta["avro.codec"]) {
	case "deflate":
		return model.CompressionGzip
	case "snappy":
		return model.CompressionSnappy
	case "zstandard", "zstd":
		return model.CompressionZstd
	default:
		return model.CompressionNone
	}
}

func renderValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprintf("%v", x)
	}
}
