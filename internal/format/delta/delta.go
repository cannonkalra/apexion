//go:build delta

// Package delta resolves Delta Lake tables by reading the _delta_log
// transaction log (the authoritative Delta schema — no data files are read).
package delta

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// Resolver detects Delta Lake tables.
type Resolver struct{}

// New returns a Delta resolver.
func New() *Resolver { return &Resolver{} }

func (r *Resolver) Format() model.Format { return model.FormatDelta }

// logAction is one line of a _delta_log/*.json commit.
type logAction struct {
	MetaData *struct {
		SchemaString     string   `json:"schemaString"`
		PartitionColumns []string `json:"partitionColumns"`
	} `json:"metaData"`
}

// sparkSchema is the StructType embedded in metaData.schemaString.
type sparkSchema struct {
	Type   string       `json:"type"`
	Fields []sparkField `json:"fields"`
}

type sparkField struct {
	Name     string          `json:"name"`
	Type     json.RawMessage `json:"type"`
	Nullable bool            `json:"nullable"`
}

// Detect looks for prefix/_delta_log/*.json and parses the latest metaData.
func (r *Resolver) Detect(ctx context.Context, cat format.Catalog, prefix string) (*format.Result, bool, error) {
	logPrefix := strings.TrimRight(prefix, "/") + "/_delta_log/"
	entries, err := cat.List(ctx, logPrefix)
	if err != nil {
		return nil, false, err
	}
	commits := jsonCommits(entries)
	if len(commits) == 0 {
		return nil, false, nil
	}
	// Scan commits newest-first for the most recent metaData action.
	for i := len(commits) - 1; i >= 0; i-- {
		raw, err := cat.Get(ctx, commits[i])
		if err != nil {
			continue
		}
		meta := findMetaData(raw)
		if meta == nil {
			continue
		}
		var ss sparkSchema
		if err := json.Unmarshal([]byte(meta.SchemaString), &ss); err != nil {
			continue
		}
		fields := make([]format.Field, 0, len(ss.Fields))
		for _, f := range ss.Fields {
			dt, phys := mapSparkType(f.Type)
			fields = append(fields, format.Field{
				Name:         f.Name,
				Type:         dt,
				PhysicalType: phys,
				Nullable:     f.Nullable,
			})
		}
		return &format.Result{
			Format:        model.FormatDelta,
			Fields:        fields,
			PartitionKeys: meta.PartitionColumns,
		}, true, nil
	}
	return nil, false, nil
}

func findMetaData(raw []byte) *struct {
	SchemaString     string   `json:"schemaString"`
	PartitionColumns []string `json:"partitionColumns"`
} {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var a logAction
		if err := json.Unmarshal(line, &a); err != nil {
			continue
		}
		if a.MetaData != nil {
			return a.MetaData
		}
	}
	return nil
}

// mapSparkType maps a Spark StructType field type (string primitive or nested
// object) to a logical type.
func mapSparkType(raw json.RawMessage) (model.DataType, string) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return mapSparkPrimitive(s), s
	}
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		switch obj.Type {
		case "struct":
			return model.TypeStruct, "struct"
		case "array":
			return model.TypeArray, "array"
		case "map":
			return model.TypeMap, "map"
		}
	}
	return model.TypeString, "string"
}

func mapSparkPrimitive(s string) model.DataType {
	switch {
	case s == "boolean":
		return model.TypeBoolean
	case s == "byte", s == "short", s == "integer", s == "long":
		return model.TypeInteger
	case s == "float", s == "double":
		return model.TypeFloat
	case strings.HasPrefix(s, "decimal"):
		return model.TypeDecimal
	case s == "date":
		return model.TypeDate
	case s == "timestamp", s == "timestamp_ntz":
		return model.TypeTimestamp
	case s == "string":
		return model.TypeString
	case s == "binary":
		return model.TypeBinary
	default:
		return model.TypeString
	}
}

// jsonCommits returns sorted _delta_log commit json files (excluding CRC/
// checkpoint files).
func jsonCommits(entries []format.Entry) []string {
	var out []string
	for _, e := range entries {
		base := e.Key
		if strings.HasSuffix(base, ".json") && !strings.Contains(base, ".checkpoint") {
			out = append(out, base)
		}
	}
	sort.Strings(out)
	return out
}

var _ = fmt.Sprintf
