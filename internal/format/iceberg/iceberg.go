//go:build iceberg

// Package iceberg resolves Apache Iceberg tables by reading their metadata.json
// (the real, authoritative Iceberg schema — no data files are read).
package iceberg

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// Resolver detects Iceberg tables.
type Resolver struct{}

// New returns an Iceberg resolver.
func New() *Resolver { return &Resolver{} }

func (r *Resolver) Format() model.Format { return model.FormatIceberg }

// metadata mirrors the parts of an Iceberg table metadata.json we consume.
type metadata struct {
	FormatVersion   int             `json:"format-version"`
	CurrentSchemaID *int            `json:"current-schema-id"`
	Schema          *icebergSchema  `json:"schema"`  // v1
	Schemas         []icebergSchema `json:"schemas"` // v2
	PartitionSpec   []specField     `json:"partition-spec"`
	PartitionSpecs  []struct {
		Fields []specField `json:"fields"`
	} `json:"partition-specs"`
	Snapshots []struct {
		Summary map[string]string `json:"summary"`
	} `json:"snapshots"`
}

type icebergSchema struct {
	SchemaID int            `json:"schema-id"`
	Fields   []icebergField `json:"fields"`
}

type icebergField struct {
	ID       int             `json:"id"`
	Name     string          `json:"name"`
	Required bool            `json:"required"`
	Type     json.RawMessage `json:"type"`
}

type specField struct {
	Name      string `json:"name"`
	SourceID  int    `json:"source-id"`
	Transform string `json:"transform"`
}

// Detect looks for prefix/metadata/*.metadata.json and parses the newest one.
func (r *Resolver) Detect(ctx context.Context, cat format.Catalog, prefix string) (*format.Result, bool, error) {
	metaPrefix := strings.TrimRight(prefix, "/") + "/metadata/"
	entries, err := cat.List(ctx, metaPrefix)
	if err != nil {
		return nil, false, err
	}
	newest := pickNewestMetadata(entries)
	if newest == "" {
		return nil, false, nil
	}
	raw, err := cat.Get(ctx, newest)
	if err != nil {
		return nil, false, fmt.Errorf("get %s: %w", newest, err)
	}
	var m metadata
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, nil // not valid Iceberg metadata
	}

	sc := m.selectSchema()
	if sc == nil {
		return nil, false, nil
	}
	fields := make([]format.Field, 0, len(sc.Fields))
	for _, f := range sc.Fields {
		dt, phys := mapIcebergType(f.Type)
		fields = append(fields, format.Field{
			Name:         f.Name,
			Type:         dt,
			PhysicalType: phys,
			Nullable:     !f.Required,
		})
	}

	res := &format.Result{
		Format:        model.FormatIceberg,
		Fields:        fields,
		PartitionKeys: m.partitionKeys(),
	}
	res.RowCountEstimate = m.estimatedRows()
	return res, true, nil
}

func (m metadata) selectSchema() *icebergSchema {
	if len(m.Schemas) > 0 {
		if m.CurrentSchemaID != nil {
			for i := range m.Schemas {
				if m.Schemas[i].SchemaID == *m.CurrentSchemaID {
					return &m.Schemas[i]
				}
			}
		}
		return &m.Schemas[0]
	}
	return m.Schema
}

func (m metadata) partitionKeys() []string {
	var out []string
	add := func(fs []specField) {
		for _, f := range fs {
			if f.Name != "" {
				out = append(out, f.Name)
			}
		}
	}
	add(m.PartitionSpec)
	for _, ps := range m.PartitionSpecs {
		add(ps.Fields)
	}
	return dedupe(out)
}

func (m metadata) estimatedRows() int64 {
	// Iceberg snapshot summaries carry total-records.
	for i := len(m.Snapshots) - 1; i >= 0; i-- {
		if v, ok := m.Snapshots[i].Summary["total-records"]; ok {
			var n int64
			fmt.Sscan(v, &n)
			return n
		}
	}
	return 0
}

// mapIcebergType maps an Iceberg type (which may be a JSON string primitive or
// a nested object) to a logical type.
func mapIcebergType(raw json.RawMessage) (model.DataType, string) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return mapIcebergPrimitive(s), s
	}
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		switch obj.Type {
		case "struct":
			return model.TypeStruct, "struct"
		case "list":
			return model.TypeArray, "list"
		case "map":
			return model.TypeMap, "map"
		}
	}
	return model.TypeString, "string"
}

func mapIcebergPrimitive(s string) model.DataType {
	switch {
	case s == "boolean":
		return model.TypeBoolean
	case s == "int", s == "long":
		return model.TypeInteger
	case s == "float", s == "double":
		return model.TypeFloat
	case strings.HasPrefix(s, "decimal"):
		return model.TypeDecimal
	case s == "date":
		return model.TypeDate
	case s == "time":
		return model.TypeTime
	case s == "timestamp", s == "timestamptz":
		return model.TypeTimestamp
	case s == "uuid":
		return model.TypeUUID
	case s == "string":
		return model.TypeString
	case s == "binary", strings.HasPrefix(s, "fixed"):
		return model.TypeBinary
	default:
		return model.TypeString
	}
}

// pickNewestMetadata chooses the highest-versioned *.metadata.json.
func pickNewestMetadata(entries []format.Entry) string {
	var metas []string
	for _, e := range entries {
		if strings.HasSuffix(e.Key, ".metadata.json") {
			metas = append(metas, e.Key)
		}
	}
	if len(metas) == 0 {
		return ""
	}
	sort.Strings(metas)
	return metas[len(metas)-1]
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
