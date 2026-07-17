// Package components renders dataviewer.ColumnProfile values into the insight
// header cells. Distributions are hand-emitted inline SVG (no canvas, no JS) so a
// wide header row with many columns stays lightweight.
package components

import (
	"fmt"

	"github.com/apexion/apexion/internal/dataviewer"
)

// SVG mini-distribution geometry (matches the h-9 skeleton height).
const (
	distW   = 112.0
	distH   = 36.0
	distGap = 2.0
)

// bar is one pre-formatted SVG rect for the mini distribution.
type bar struct {
	X, Y, W, H string
	Title      string
}

// distBars picks the distribution to draw — numeric histogram, else categorical
// top-N — and lays it out as SVG rects. Returns nil when there's nothing to draw.
func distBars(p dataviewer.ColumnProfile) []bar {
	if p.Kind == dataviewer.KindNumeric && len(p.Histogram) > 0 {
		counts := make([]int64, len(p.Histogram))
		labels := make([]string, len(p.Histogram))
		for i, b := range p.Histogram {
			counts[i], labels[i] = b.Count, b.Label
		}
		return layoutBars(counts, labels)
	}
	if len(p.TopValues) > 0 {
		counts := make([]int64, len(p.TopValues))
		labels := make([]string, len(p.TopValues))
		for i, v := range p.TopValues {
			counts[i], labels[i] = v.Count, v.Value
		}
		return layoutBars(counts, labels)
	}
	return nil
}

func layoutBars(counts []int64, labels []string) []bar {
	n := len(counts)
	if n == 0 {
		return nil
	}
	var max int64 = 1
	for _, c := range counts {
		if c > max {
			max = c
		}
	}
	bw := (distW - distGap*float64(n-1)) / float64(n)
	if bw < 1 {
		bw = 1
	}
	out := make([]bar, n)
	for i, c := range counts {
		bh := distH * float64(c) / float64(max)
		if c > 0 && bh < 1 {
			bh = 1 // keep tiny non-zero bars visible
		}
		lbl := ""
		if i < len(labels) {
			lbl = labels[i]
		}
		out[i] = bar{
			X:     fmtF(float64(i) * (bw + distGap)),
			Y:     fmtF(distH - bh),
			W:     fmtF(bw),
			H:     fmtF(bh),
			Title: fmt.Sprintf("%s (%d)", lbl, c),
		}
	}
	return out
}

func fmtF(v float64) string { return fmt.Sprintf("%.2f", v) }

// nullPctText / distinctPctText format the header's percentage line.
func nullPctText(p dataviewer.ColumnProfile) string {
	if p.Count == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%% null", p.NullPct*100)
}

func distinctPctText(p dataviewer.ColumnProfile) string {
	if p.NonNull == 0 {
		return ""
	}
	return fmt.Sprintf("%.0f%% distinct", p.DistinctPct*100)
}

// badgeClass maps a badge tone to Tailwind colour utilities.
func badgeClass(tone string) string {
	switch tone {
	case "warn":
		return "bg-accent-amber/15 text-accent-amber"
	case "muted":
		return "bg-base-700 text-slate-400"
	default: // "info"
		return "bg-brand-500/15 text-brand-300"
	}
}
