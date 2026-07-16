// Package json implements a schema reader for JSON (array-of-objects) and
// JSONL (newline-delimited objects). Nested objects and arrays are recognized
// and typed as struct/array/json; scalars are inferred normally.
package json

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// Reader reads JSON or JSONL.
type Reader struct{ format model.Format }

// NewJSON returns a reader for pretty/array JSON.
func NewJSON() *Reader { return &Reader{format: model.FormatJSON} }

// NewJSONL returns a reader for newline-delimited JSON.
func NewJSONL() *Reader { return &Reader{format: model.FormatJSONL} }

func (r *Reader) Format() model.Format { return r.format }

func (r *Reader) ReadSchema(ctx context.Context, src format.Source, opts format.Options) (*format.Result, error) {
	rc, err := src.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer rc.Close()

	comp := format.DetectCompression(src.Key())
	limit := opts.SampleBytes
	if limit <= 0 {
		limit = 262144
	}
	if comp != model.CompressionNone && comp != "" {
		limit *= 4
	}
	dr, err := format.Decompress(io.LimitReader(rc, limit), comp)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}

	maxRows := opts.SampleRows
	if maxRows <= 0 {
		maxRows = 1000
	}

	var objects []map[string]json.RawMessage
	if r.format == model.FormatJSONL {
		objects = readJSONL(dr, maxRows)
	} else {
		objects = readJSONArray(dr, maxRows)
		if len(objects) == 0 {
			// A .json file may actually be line-delimited; retry.
			rc2, e := src.Open(ctx)
			if e == nil {
				defer rc2.Close()
				dr2, e2 := format.Decompress(io.LimitReader(rc2, limit), comp)
				if e2 == nil {
					objects = readJSONL(dr2, maxRows)
				}
			}
		}
	}
	if len(objects) == 0 {
		return &format.Result{Format: r.format, Compression: comp}, nil
	}

	// Union of keys, stable ordered by first appearance then name.
	order := []string{}
	seen := map[string]bool{}
	for _, o := range objects {
		for k := range o {
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	sort.Strings(order)

	colValues := make(map[string][]string, len(order))
	colKinds := make(map[string][]model.DataType, len(order))
	var rows [][]string
	for _, o := range objects {
		row := make([]string, len(order))
		for i, k := range order {
			raw, ok := o[k]
			if !ok {
				colValues[k] = append(colValues[k], "")
				continue
			}
			cell, kind := renderJSON(raw)
			row[i] = cell
			colValues[k] = append(colValues[k], cell)
			colKinds[k] = append(colKinds[k], kind)
		}
		rows = append(rows, row)
	}

	fields := make([]format.Field, len(order))
	for i, k := range order {
		// Prefer structural kind (struct/array) when present, else infer scalar.
		dt, nullable := reconcile(colKinds[k], colValues[k])
		fields[i] = format.Field{
			Name:         k,
			Type:         dt,
			PhysicalType: string(dt),
			Nullable:     nullable || len(colValues[k]) < len(objects),
		}
	}

	return &format.Result{
		Format:           r.format,
		Compression:      comp,
		Fields:           fields,
		Rows:             rows,
		RowCountEstimate: estimateRows(src.Size(), len(objects), rows),
	}, nil
}

func readJSONL(r io.Reader, maxRows int) []map[string]json.RawMessage {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var out []map[string]json.RawMessage
	for sc.Scan() && len(out) < maxRows {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(line, &m); err == nil && m != nil {
			out = append(out, m)
		}
	}
	return out
}

func readJSONArray(r io.Reader, maxRows int) []map[string]json.RawMessage {
	dec := json.NewDecoder(bufio.NewReader(r))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	var out []map[string]json.RawMessage
	if d, ok := tok.(json.Delim); ok && d == '[' {
		for dec.More() && len(out) < maxRows {
			var m map[string]json.RawMessage
			if err := dec.Decode(&m); err != nil {
				break
			}
			if m != nil {
				out = append(out, m)
			}
		}
		return out
	}
	// Single object.
	if d, ok := tok.(json.Delim); ok && d == '{' {
		// Re-decode from scratch is simpler: not worth it; treat as one row.
	}
	return out
}

// renderJSON turns a raw JSON value into a string cell and its logical type.
func renderJSON(raw json.RawMessage) (string, model.DataType) {
	trimmed := trimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "", model.TypeNull
	}
	switch trimmed[0] {
	case '{':
		return string(trimmed), model.TypeStruct
	case '[':
		return string(trimmed), model.TypeArray
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s, format.InferCellType(s)
		}
		return string(trimmed), model.TypeString
	case 't', 'f':
		return string(trimmed), model.TypeBoolean
	default:
		s := string(trimmed)
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return s, model.TypeInteger
		}
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return s, model.TypeFloat
		}
		return s, model.TypeString
	}
}

func reconcile(kinds []model.DataType, values []string) (model.DataType, bool) {
	current := model.TypeUnknown
	nullable := false
	for _, k := range kinds {
		if k == model.TypeNull {
			nullable = true
			continue
		}
		current = format.MergeTypes(current, k)
	}
	if current == model.TypeUnknown {
		return format.InferColumnType(values)
	}
	return current, nullable
}

func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}

func estimateRows(fileSize int64, sampled int, rows [][]string) int64 {
	if sampled == 0 {
		return 0
	}
	var sampledBytes int64
	for _, r := range rows {
		for _, c := range r {
			sampledBytes += int64(len(c)) + 3
		}
	}
	if sampledBytes == 0 || fileSize <= sampledBytes {
		return int64(sampled)
	}
	perRow := float64(sampledBytes) / float64(sampled)
	return int64(float64(fileSize) / perRow)
}
