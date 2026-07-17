package dataviewer

import (
	"regexp"
	"strings"
)

// Badge detection runs over the tiny sample (scalar stats + a handful of sample
// cells), never a full scan, so Go-side regex is cheap. At most maxBadges are
// surfaced per column so a wide header row stays legible.
const maxBadges = 2

var (
	uuidRe  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// Derive returns the quality/pattern badges for a profile, in priority order
// (Constant → Candidate Key → UUID/Email → Mostly Null → High Cardinality),
// capped at maxBadges. sampleCells are the column's non-null-ish preview values
// used for regex pattern detection.
func Derive(p *ColumnProfile, sampleCells []string) []Badge {
	badges := make([]Badge, 0, maxBadges)
	add := func(label, tone string) {
		if len(badges) < maxBadges {
			badges = append(badges, Badge{Label: label, Tone: tone})
		}
	}

	complete := p.Count > 0 && p.NonNull == p.Count
	// approx_count_distinct is approximate; treat ~unique as a candidate key.
	candidateKey := complete && p.NonNull >= 20 && p.DistinctPct >= 0.99

	if p.NonNull > 0 && p.Distinct <= 1 {
		add("Constant", "muted")
	}
	if candidateKey {
		add("Candidate Key", "info")
	}
	if p.Kind == KindString {
		switch {
		case matchFrac(sampleCells, uuidRe) >= 0.9:
			add("UUID", "info")
		case matchFrac(sampleCells, emailRe) >= 0.9:
			add("Email", "info")
		}
	}
	if p.NullPct >= 0.5 {
		add("Mostly Null", "warn")
	}
	if !candidateKey && p.NonNull >= 20 && p.DistinctPct >= 0.9 {
		add("High Cardinality", "info")
	}
	return badges
}

// matchFrac is the fraction of non-empty sample cells matching re.
func matchFrac(cells []string, re *regexp.Regexp) float64 {
	var total, matched int
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		total++
		if re.MatchString(c) {
			matched++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(matched) / float64(total)
}
