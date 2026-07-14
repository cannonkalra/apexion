// Package inference is the schema/semantic inference engine. Given a sample of
// rows it derives logical types, semantic types, PII flags, primary-key and
// foreign-key candidates, and data-quality metrics.
package inference

import (
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
	return res
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
