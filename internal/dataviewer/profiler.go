package dataviewer

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/duckdb"
)

// maxDistribCols caps how wide a table still gets distribution profiling
// (top-N + histograms). Beyond it, only null/distinct and badges are computed,
// so the batched queries and header row stay bounded on very wide tables.
const maxDistribCols = 40

// cacheTTL bounds how long a computed profile is reused.
const cacheTTL = 5 * time.Minute

// Profiler computes column insights over a bounded DuckDB sample and caches them.
type Profiler struct {
	eng   *duckdb.Engine
	cache *cache
	log   zerolog.Logger
}

// New builds a Profiler over the shared preview engine.
func New(eng *duckdb.Engine, log zerolog.Logger) *Profiler {
	return &Profiler{
		eng:   eng,
		cache: newCache(cacheTTL),
		log:   log.With().Str("component", "dataviewer").Logger(),
	}
}

// Base returns bare profiles (name/type/kind only) for a preview result, used to
// render an aligned insight row when full profiling is unavailable.
func Base(res *duckdb.Result) []ColumnProfile {
	profiles := make([]ColumnProfile, len(res.Columns))
	for i, name := range res.Columns {
		t := ""
		if i < len(res.Types) {
			t = res.Types[i]
		}
		profiles[i] = ColumnProfile{Name: name, Type: t, Kind: classify(t)}
	}
	return profiles
}

// Profile computes per-column insights for src over a sample bounded by
// sampleCap. res is the already-rendered preview result: it supplies the column
// list/types and the sample cells used for Go-side histograms and pattern
// detection. Statistics and top-N come from DuckDB (2 round-trips at most).
// Results are cached by the reconstructed reader expression + sampleCap.
func (p *Profiler) Profile(ctx context.Context, src Source, res *duckdb.Result, sampleCap int) ([]ColumnProfile, error) {
	opts := src.Opts
	opts.Filename = false
	from, err := duckdb.FromClause(src.Bucket, src.Key, src.Format, opts)
	if err != nil {
		return Base(res), err
	}

	now := time.Now()
	key := cacheKey(from, sampleCap)
	if cached, ok := p.cache.get(key, now); ok {
		return cached, nil
	}

	profiles := Base(res)

	// Query A — batched scalar statistics for all columns (1 round-trip).
	statsSQL, plan := buildStatsSQL(profiles, from, sampleCap)
	if resA, err := p.eng.QueryProfile(ctx, statsSQL); err != nil {
		p.log.Warn().Err(err).Msg("scalar stats query failed")
	} else if len(resA.Rows) > 0 {
		applyStatsRow(resA.Rows[0], plan, profiles)
	}
	for i := range profiles {
		finalizePct(&profiles[i])
	}

	// Distribution (Query B top-N + Go-side histograms), skipped on very wide
	// tables to bound cost.
	if len(profiles) <= maxDistribCols {
		if topSQL, ok := buildTopNSQL(profiles, from, sampleCap); ok {
			if resB, err := p.eng.QueryProfile(ctx, topSQL); err != nil {
				p.log.Warn().Err(err).Msg("top-N query failed")
			} else {
				applyTopN(resB, profiles)
			}
		}
		buildHistograms(res, profiles)
	}

	// Quality/pattern badges from stats + preview sample cells.
	for i := range profiles {
		profiles[i].Badges = Derive(&profiles[i], columnCells(res, i))
	}

	p.cache.put(key, profiles, now)
	return profiles, nil
}

func finalizePct(p *ColumnProfile) {
	if p.Count > 0 {
		p.NullPct = float64(p.Count-p.NonNull) / float64(p.Count)
	}
	if p.NonNull > 0 {
		p.DistinctPct = float64(p.Distinct) / float64(p.NonNull)
		if p.DistinctPct > 1 { // approx_count_distinct can slightly overshoot
			p.DistinctPct = 1
		}
	}
}

// columnCells collects a column's preview values for regex pattern detection.
func columnCells(res *duckdb.Result, col int) []string {
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if col < len(row) {
			out = append(out, row[col])
		}
	}
	return out
}
