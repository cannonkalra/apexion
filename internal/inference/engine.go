// Package inference is the schema/semantic inference engine. Given a sample of
// rows it derives logical types, semantic types, PII flags, primary-key and
// foreign-key candidates, and data-quality metrics.
package inference

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apexion/apexion/internal/crawler/format"
	"github.com/apexion/apexion/internal/model"
)

// RefColumn is a candidate reference (another dataset's key column) used for
// foreign-key detection.
type RefColumn struct {
	DatasetID   string
	DatasetName string
	Column      string
	Values      map[string]struct{}
}

// Input is the material the engine analyzes.
type Input struct {
	DatasetID       string
	DatasetName     string
	Format          model.Format
	PartitionKeys   []string
	Fields          []format.Field
	Rows            [][]string
	RefColumns      []RefColumn
	MaxSampleValues int
	DetectPII       bool
}

// Engine performs inference. It is stateless and safe for concurrent use.
type Engine struct{}

// NewEngine constructs an inference engine.
func NewEngine() *Engine { return &Engine{} }

// Analyze runs the full inference pipeline over the input sample.
func (e *Engine) Analyze(in Input) model.InferenceResult {
	if in.MaxSampleValues <= 0 {
		in.MaxSampleValues = 10
	}
	total := len(in.Rows)
	cols := make([]model.ColumnInference, len(in.Fields))

	var sumCompleteness, sumUniqueness, sumValidity float64
	consistentCols := 0
	var piiCols []string

	for j, f := range in.Fields {
		values := columnValues(in.Rows, j)
		nonNull := filterNonNull(values)

		ci := model.ColumnInference{
			Name:     f.Name,
			DataType: f.Type,
			Nullable: f.Nullable || len(nonNull) < total,
		}

		if total > 0 {
			ci.Completeness = float64(len(nonNull)) / float64(total)
		}
		distinct := distinctSet(nonNull)
		ci.DistinctCount = int64(len(distinct))
		if len(nonNull) > 0 {
			ci.Uniqueness = float64(len(distinct)) / float64(len(nonNull))
		}
		ci.MinValue, ci.MaxValue = minMax(nonNull, f.Type)
		ci.SampleValues = sampleDistinct(distinct, in.MaxSampleValues)

		// Semantic classification. Temporal and boolean columns are typed
		// directly (never PII), which also prevents date strings from matching
		// the phone/number detectors.
		switch f.Type {
		case model.TypeDate, model.TypeTimestamp, model.TypeTime:
			ci.SemanticType = model.SemanticDateTime
			if len(nonNull) > 0 {
				if _, dt := format.DetectDateLayout(nonNull[0]); dt != model.TypeUnknown {
					ci.Confidence = 1
				}
			}
		case model.TypeBoolean, model.TypeStruct, model.TypeArray, model.TypeMap, model.TypeJSON:
			// leave semantic unset
		default:
			if in.DetectPII {
				sem, pii, conf := classifySemantic(f.Name, nonNull)
				ci.SemanticType = sem
				ci.IsPII = pii
				ci.Confidence = conf
				if pii {
					piiCols = append(piiCols, f.Name)
				}
			}
		}

		// Date format detection.
		if f.Type == model.TypeDate || f.Type == model.TypeTimestamp || f.Type == model.TypeTime {
			if len(nonNull) > 0 {
				if layout, _ := format.DetectDateLayout(nonNull[0]); layout != "" {
					ci.DateFormat = layout
				}
			}
		}

		// Primary-key candidate: fully populated, fully unique, non-float.
		ci.IsPKCandidate = ci.Completeness >= 0.999 && ci.Uniqueness >= 0.999 &&
			ci.DistinctCount > 1 && f.Type != model.TypeFloat && f.Type != model.TypeDecimal

		// Validity: fraction of non-null values compatible with the column type.
		validity := columnValidity(nonNull, f.Type)

		sumCompleteness += ci.Completeness
		sumUniqueness += ci.Uniqueness
		sumValidity += validity
		if isConsistent(nonNull, f.Type) {
			consistentCols++
		}

		cols[j] = ci
	}

	res := model.InferenceResult{
		DatasetID:   in.DatasetID,
		SampleRows:  total,
		Columns:     cols,
		PrimaryKeys: choosePrimaryKeys(cols),
		ForeignKeys: detectForeignKeys(in, cols),
		PIIColumns:  piiCols,
		GeneratedAt: time.Now().UTC(),
	}

	n := float64(len(in.Fields))
	if n > 0 {
		res.QualityMetrics = model.QualityMetrics{
			Completeness: sumCompleteness / n,
			Uniqueness:   sumUniqueness / n,
			Validity:     sumValidity / n,
			Consistency:  float64(consistentCols) / n,
			Rows:         total,
			Columns:      len(in.Fields),
		}
	}
	res.QualityScore = qualityScore(res.QualityMetrics)

	// Extended Phase-1 analyses.
	res.MissingValues = missingValues(cols, total)
	res.DuplicateAnalysis = duplicateAnalysis(in.Rows)
	res.RecommendedPartitions = recommendPartitions(cols, in.PartitionKeys, total)
	res.DorisSchema = dorisSchema(in.DatasetName, cols, res.PrimaryKeys, res.RecommendedPartitions)
	res.SparkOptimizations = sparkOptimizations(in.Format, res.RecommendedPartitions, cols)
	res.FlinkOptimizations = flinkOptimizations(in.Format, cols)
	res.DatasetSummary = datasetSummary(in, res)
	res.BusinessDescription = res.DatasetSummary
	return res
}

func missingValues(cols []model.ColumnInference, total int) []model.MissingValueStat {
	out := make([]model.MissingValueStat, 0, len(cols))
	for _, c := range cols {
		nullRatio := 1 - c.Completeness
		nullCount := int(nullRatio*float64(total) + 0.5)
		out = append(out, model.MissingValueStat{
			Column: c.Name, NullCount: nullCount, NullRatio: round2(nullRatio),
		})
	}
	return out
}

func duplicateAnalysis(rows [][]string) model.DuplicateAnalysis {
	total := len(rows)
	seen := make(map[string]struct{}, total)
	for _, r := range rows {
		seen[strings.Join(r, "\x1f")] = struct{}{}
	}
	unique := len(seen)
	dup := total - unique
	da := model.DuplicateAnalysis{TotalRows: total, DuplicateRows: dup, UniqueRows: unique}
	if total > 0 {
		da.DuplicateRatio = round2(float64(dup) / float64(total))
	}
	return da
}

// recommendPartitions suggests good partition columns: existing partition keys,
// plus low-cardinality categorical/temporal columns.
func recommendPartitions(cols []model.ColumnInference, existing []string, total int) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, k := range existing {
		add(k)
	}
	maxCard := total / 4
	if maxCard < 2 {
		maxCard = 2
	}
	if maxCard > 100 {
		maxCard = 100
	}
	for _, c := range cols {
		if c.IsPII || c.IsPKCandidate {
			continue
		}
		switch c.DataType {
		case model.TypeDate, model.TypeTimestamp:
			add(c.Name)
		case model.TypeString, model.TypeInteger, model.TypeBoolean:
			if c.DistinctCount >= 2 && int(c.DistinctCount) <= maxCard {
				add(c.Name)
			}
		}
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// dorisSchema renders a suggested Apache Doris CREATE TABLE statement.
func dorisSchema(name string, cols []model.ColumnInference, pks, partitions []string) string {
	if name == "" {
		name = "dataset"
	}
	table := sanitizeIdent(name)
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", table)
	for i, c := range cols {
		comma := ","
		if i == len(cols)-1 {
			comma = ""
		}
		fmt.Fprintf(&b, "    `%s` %s%s\n", c.Name, dorisType(c.DataType), comma)
	}
	b.WriteString(")\n")
	keyCols := pks
	if len(keyCols) == 0 && len(cols) > 0 {
		keyCols = []string{cols[0].Name}
	}
	fmt.Fprintf(&b, "DUPLICATE KEY(`%s`)\n", strings.Join(keyCols, "`, `"))
	if len(partitions) > 0 {
		fmt.Fprintf(&b, "-- suggested partitioning by: %s\n", strings.Join(partitions, ", "))
	}
	fmt.Fprintf(&b, "DISTRIBUTED BY HASH(`%s`) BUCKETS 10\n", keyCols[0])
	b.WriteString("PROPERTIES (\"replication_num\" = \"1\");")
	return b.String()
}

func dorisType(t model.DataType) string {
	switch t {
	case model.TypeInteger:
		return "BIGINT"
	case model.TypeFloat:
		return "DOUBLE"
	case model.TypeDecimal:
		return "DECIMAL(38, 9)"
	case model.TypeBoolean:
		return "BOOLEAN"
	case model.TypeDate:
		return "DATE"
	case model.TypeTimestamp:
		return "DATETIME"
	case model.TypeJSON, model.TypeStruct, model.TypeMap, model.TypeArray:
		return "JSONB"
	default:
		return "VARCHAR(65533)"
	}
}

func sparkOptimizations(f model.Format, partitions []string, cols []model.ColumnInference) []string {
	var recs []string
	switch f {
	case model.FormatParquet, model.FormatIceberg, model.FormatDelta:
		recs = append(recs, "Enable predicate & projection pushdown (spark.sql.parquet.filterPushdown=true)")
		recs = append(recs, "Enable vectorized reader (spark.sql.parquet.enableVectorizedReader=true)")
	case model.FormatCSV, model.FormatTSV, model.FormatJSON, model.FormatJSONL:
		recs = append(recs, "Convert to Parquet/Delta for columnar scans and pushdown")
		recs = append(recs, "Provide an explicit schema to avoid costly inference on read")
	}
	recs = append(recs, "Enable Adaptive Query Execution (spark.sql.adaptive.enabled=true)")
	if len(partitions) > 0 {
		recs = append(recs, fmt.Sprintf("Partition writes by %s to enable partition pruning", strings.Join(partitions, ", ")))
	}
	if hasType(cols, model.TypeString) {
		recs = append(recs, "Broadcast small dimension tables to avoid shuffles on string joins")
	}
	return recs
}

func flinkOptimizations(f model.Format, cols []model.ColumnInference) []string {
	var recs []string
	switch f {
	case model.FormatParquet, model.FormatIceberg, model.FormatDelta:
		recs = append(recs, "Use the FileSystem/Iceberg connector with format='parquet' and filter pushdown")
	default:
		recs = append(recs, "Use the FileSystem connector; prefer Parquet source for streaming reads")
	}
	if ts := firstOfType(cols, model.TypeTimestamp); ts != "" {
		recs = append(recs, fmt.Sprintf("Define an event-time watermark on `%s` for windowed aggregations", ts))
	}
	recs = append(recs, "Enable incremental checkpointing and tune parallelism to source splits")
	recs = append(recs, "Enable mini-batch aggregation (table.exec.mini-batch.enabled=true) to reduce state access")
	return recs
}

func datasetSummary(in Input, res model.InferenceResult) string {
	name := in.DatasetName
	if name == "" {
		name = "This dataset"
	}
	parts := []string{fmt.Sprintf("%s is a %s dataset with %d columns", name, in.Format, len(res.Columns))}
	if len(res.PrimaryKeys) > 0 {
		parts = append(parts, fmt.Sprintf("keyed by %s", strings.Join(res.PrimaryKeys, ", ")))
	}
	if len(res.PIIColumns) > 0 {
		parts = append(parts, fmt.Sprintf("containing PII in %d column(s)", len(res.PIIColumns)))
	}
	parts = append(parts, fmt.Sprintf("with an overall quality score of %.0f/100", res.QualityScore))
	return strings.Join(parts, ", ") + "."
}

func hasType(cols []model.ColumnInference, t model.DataType) bool {
	for _, c := range cols {
		if c.DataType == t {
			return true
		}
	}
	return false
}

func firstOfType(cols []model.ColumnInference, t model.DataType) string {
	for _, c := range cols {
		if c.DataType == t {
			return c.Name
		}
	}
	return ""
}

func sanitizeIdent(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "t_" + out
	}
	return out
}

// qualityScore blends the quality dimensions into a 0..100 score.
func qualityScore(m model.QualityMetrics) float64 {
	score := 0.40*m.Completeness + 0.20*m.Uniqueness + 0.25*m.Validity + 0.15*m.Consistency
	return round1(score * 100)
}

func choosePrimaryKeys(cols []model.ColumnInference) []string {
	var candidates []model.ColumnInference
	for _, c := range cols {
		if c.IsPKCandidate {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// Prefer columns whose name looks like an identifier.
	sort.SliceStable(candidates, func(i, j int) bool {
		return pkNameScore(candidates[i].Name) > pkNameScore(candidates[j].Name)
	})
	// Report the single strongest candidate plus any other id-named unique cols.
	var out []string
	out = append(out, candidates[0].Name)
	for _, c := range candidates[1:] {
		if pkNameScore(c.Name) >= 2 {
			out = append(out, c.Name)
		}
	}
	return out
}

func pkNameScore(name string) int {
	l := strings.ToLower(name)
	switch {
	case l == "id":
		return 3
	case strings.HasSuffix(l, "_id"), strings.HasSuffix(l, "id"), l == "uuid", l == "pk", strings.HasSuffix(l, "_key"):
		return 2
	default:
		return 1
	}
}

func detectForeignKeys(in Input, cols []model.ColumnInference) []model.ForeignKeyCandidate {
	var out []model.ForeignKeyCandidate
	for j, f := range in.Fields {
		values := distinctSet(filterNonNull(columnValues(in.Rows, j)))
		if len(values) == 0 {
			continue
		}
		for _, ref := range in.RefColumns {
			if ref.DatasetID == in.DatasetID {
				continue
			}
			// Name affinity: <ref>_id or matching column name raises confidence.
			nameAffinity := fkNameAffinity(f.Name, ref)
			overlap := overlapRatio(values, ref.Values)
			if overlap < 0.5 && nameAffinity < 0.5 {
				continue
			}
			conf := 0.6*overlap + 0.4*nameAffinity
			if conf >= 0.5 {
				out = append(out, model.ForeignKeyCandidate{
					Column:       f.Name,
					RefDataset:   ref.DatasetName,
					RefColumn:    ref.Column,
					OverlapRatio: round2(overlap),
					Confidence:   round2(conf),
				})
			}
		}
	}
	// Keep the strongest candidate per column.
	return topPerColumn(out)
}

func fkNameAffinity(col string, ref RefColumn) float64 {
	l := strings.ToLower(col)
	rd := strings.ToLower(strings.TrimRight(ref.DatasetName, "s"))
	switch {
	case l == strings.ToLower(ref.Column) && ref.Column != "id":
		return 1
	case strings.HasPrefix(l, rd) && strings.HasSuffix(l, "id"):
		return 1
	case strings.Contains(l, rd) && strings.Contains(l, "id"):
		return 0.8
	default:
		return 0
	}
}

func overlapRatio(vals map[string]struct{}, ref map[string]struct{}) float64 {
	if len(vals) == 0 || len(ref) == 0 {
		return 0
	}
	hit := 0
	for v := range vals {
		if _, ok := ref[v]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(vals))
}

func topPerColumn(in []model.ForeignKeyCandidate) []model.ForeignKeyCandidate {
	best := map[string]model.ForeignKeyCandidate{}
	for _, c := range in {
		if cur, ok := best[c.Column]; !ok || c.Confidence > cur.Confidence {
			best[c.Column] = c
		}
	}
	out := make([]model.ForeignKeyCandidate, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	return out
}

// ---- helpers -------------------------------------------------------------

func columnValues(rows [][]string, j int) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if j < len(r) {
			out = append(out, r[j])
		} else {
			out = append(out, "")
		}
	}
	return out
}

func filterNonNull(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !format.IsNull(v) {
			out = append(out, v)
		}
	}
	return out
}

func distinctSet(values []string) map[string]struct{} {
	m := make(map[string]struct{}, len(values))
	for _, v := range values {
		m[v] = struct{}{}
	}
	return m
}

func sampleDistinct(set map[string]struct{}, n int) []string {
	out := make([]string, 0, n)
	for v := range set {
		out = append(out, v)
		if len(out) >= n {
			break
		}
	}
	sort.Strings(out)
	return out
}

func minMax(values []string, dt model.DataType) (string, string) {
	if len(values) == 0 {
		return "", ""
	}
	numeric := dt == model.TypeInteger || dt == model.TypeFloat || dt == model.TypeDecimal
	if numeric {
		var min, max float64
		set := false
		var minS, maxS string
		for _, v := range values {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				continue
			}
			if !set || f < min {
				min, minS, set = f, v, true
			}
			if f > max {
				max, maxS = f, v
			}
		}
		if set {
			if maxS == "" {
				maxS = minS
			}
			return minS, maxS
		}
	}
	minS, maxS := values[0], values[0]
	for _, v := range values {
		if v < minS {
			minS = v
		}
		if v > maxS {
			maxS = v
		}
	}
	return minS, maxS
}

func columnValidity(values []string, dt model.DataType) float64 {
	if len(values) == 0 {
		return 1
	}
	valid := 0
	for _, v := range values {
		if isCompatible(format.InferCellType(v), dt) {
			valid++
		}
	}
	return float64(valid) / float64(len(values))
}

func isConsistent(values []string, dt model.DataType) bool {
	return columnValidity(values, dt) >= 0.95
}

// isCompatible reports whether an observed cell type fits the column's type.
func isCompatible(cell, col model.DataType) bool {
	if cell == col || cell == model.TypeNull {
		return true
	}
	switch col {
	case model.TypeFloat, model.TypeDecimal:
		return cell == model.TypeInteger || cell == model.TypeFloat || cell == model.TypeDecimal
	case model.TypeTimestamp:
		return cell == model.TypeDate || cell == model.TypeTimestamp
	case model.TypeString, model.TypeJSON:
		return true // strings accept anything
	default:
		return false
	}
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
