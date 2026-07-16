// Package csv implements a streaming CSV/TSV schema reader. It never loads the
// whole file: it reads a bounded byte sample and parses sample rows from it.
package csv

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/apexion/apexion/internal/format"
	"github.com/apexion/apexion/internal/model"
)

// Reader reads CSV or TSV depending on the format it was constructed for.
type Reader struct {
	format    model.Format
	delimiter rune
}

// NewCSV returns a comma-delimited reader.
func NewCSV() *Reader { return &Reader{format: model.FormatCSV, delimiter: ','} }

// NewTSV returns a tab-delimited reader.
func NewTSV() *Reader { return &Reader{format: model.FormatTSV, delimiter: '\t'} }

func (r *Reader) Format() model.Format { return r.format }

// ReadSchema samples the file, detects the header, and infers per-column types.
func (r *Reader) ReadSchema(ctx context.Context, src format.Source, opts format.Options) (*format.Result, error) {
	rc, err := src.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer rc.Close()

	comp := format.DetectCompression(src.Key())
	dr, err := format.Decompress(io.LimitReader(rc, sampleLimit(opts, comp)), comp)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}

	delim := r.delimiter
	if opts.Delimiter != 0 && r.format == model.FormatCSV {
		delim = opts.Delimiter
	}

	cr := csv.NewReader(bufio.NewReader(dr))
	cr.Comma = delim
	cr.FieldsPerRecord = -1 // tolerate ragged rows
	cr.LazyQuotes = true
	cr.ReuseRecord = false

	maxRows := opts.SampleRows
	if maxRows <= 0 {
		maxRows = 1000
	}

	var records [][]string
	for len(records) <= maxRows {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Truncated final record from the byte-limited sample: stop cleanly.
			if len(records) > 0 {
				break
			}
			return nil, fmt.Errorf("parse csv: %w", err)
		}
		records = append(records, rec)
	}
	if len(records) == 0 {
		return &format.Result{Format: r.format, Compression: comp}, nil
	}

	header, dataStart := detectHeader(records)
	cols := len(header)

	// Collect sample cells per column.
	colValues := make([][]string, cols)
	var rows [][]string
	for _, rec := range records[dataStart:] {
		row := make([]string, cols)
		for i := 0; i < cols; i++ {
			if i < len(rec) {
				row[i] = rec[i]
				colValues[i] = append(colValues[i], rec[i])
			}
		}
		rows = append(rows, row)
	}

	fields := make([]format.Field, cols)
	for i := 0; i < cols; i++ {
		dt, nullable := format.InferColumnType(colValues[i])
		fields[i] = format.Field{
			Name:         header[i],
			Type:         dt,
			PhysicalType: "string", // CSV is untyped on disk
			Nullable:     nullable,
		}
	}

	return &format.Result{
		Format:           r.format,
		Compression:      comp,
		Fields:           fields,
		Rows:             rows,
		RowCountEstimate: estimateRows(src.Size(), records),
	}, nil
}

func sampleLimit(opts format.Options, comp model.Compression) int64 {
	limit := opts.SampleBytes
	if limit <= 0 {
		limit = 262144
	}
	// Compressed inputs expand; read more compressed bytes to get enough rows.
	if comp != model.CompressionNone && comp != "" {
		limit *= 4
	}
	return limit
}

// detectHeader decides whether the first record is a header row. Heuristic: a
// header is present when the first row is all non-numeric strings while at
// least one later row contains a numeric value.
func detectHeader(records [][]string) (header []string, dataStart int) {
	first := records[0]
	firstAllText := true
	for _, c := range first {
		if c == "" {
			continue
		}
		if _, err := strconv.ParseFloat(strings.TrimSpace(c), 64); err == nil {
			firstAllText = false
			break
		}
	}
	laterHasNumber := false
	for _, rec := range records[1:] {
		for _, c := range rec {
			if _, err := strconv.ParseFloat(strings.TrimSpace(c), 64); err == nil {
				laterHasNumber = true
				break
			}
		}
		if laterHasNumber {
			break
		}
	}
	if firstAllText && (laterHasNumber || len(records) == 1) {
		return sanitizeHeader(first), 1
	}
	// No header: synthesize column names.
	names := make([]string, len(first))
	for i := range names {
		names[i] = fmt.Sprintf("col_%d", i+1)
	}
	return names, 0
}

func sanitizeHeader(raw []string) []string {
	out := make([]string, len(raw))
	seen := map[string]int{}
	for i, h := range raw {
		name := strings.TrimSpace(h)
		if name == "" {
			name = fmt.Sprintf("col_%d", i+1)
		}
		if n, ok := seen[name]; ok {
			seen[name] = n + 1
			name = fmt.Sprintf("%s_%d", name, n+1)
		} else {
			seen[name] = 1
		}
		out[i] = name
	}
	return out
}

// estimateRows extrapolates total rows from the sampled bytes vs file size.
func estimateRows(fileSize int64, sample [][]string) int64 {
	if len(sample) == 0 {
		return 0
	}
	var sampledBytes int64
	for _, rec := range sample {
		for _, c := range rec {
			sampledBytes += int64(len(c)) + 1
		}
		sampledBytes++
	}
	if sampledBytes == 0 || fileSize <= sampledBytes {
		return int64(len(sample))
	}
	perRow := float64(sampledBytes) / float64(len(sample))
	return int64(float64(fileSize) / perRow)
}
