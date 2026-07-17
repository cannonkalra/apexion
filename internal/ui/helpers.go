package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/apexion/apexion/internal/model"
)

// humanBytes formats a byte count as a human-readable string.
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	f := float64(n)
	i := -1
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// humanCount abbreviates large counts (1.2K, 3.4M).
func humanCount(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	default:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	}
}

// timeAgo renders a relative time.
func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func timeAgoPtr(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return timeAgo(*t)
}

// formatBadge returns Tailwind classes for a format badge.
func formatBadge(f model.Format) string {
	switch f {
	case model.FormatParquet:
		return "bg-brand-500/15 text-brand-300"
	case model.FormatCSV, model.FormatTSV:
		return "bg-accent-emerald/15 text-accent-emerald"
	case model.FormatJSON, model.FormatJSONL:
		return "bg-accent-amber/15 text-accent-amber"
	case model.FormatAvro, model.FormatORC:
		return "bg-accent-violet/15 text-accent-violet"
	case model.FormatIceberg, model.FormatDelta:
		return "bg-accent-rose/15 text-accent-rose"
	default:
		return "bg-base-700 text-slate-300"
	}
}

// typeBadge returns classes for a data-type badge.
func typeBadge(t model.DataType) string {
	switch t {
	case model.TypeInteger, model.TypeFloat, model.TypeDecimal:
		return "bg-brand-500/15 text-brand-300"
	case model.TypeString:
		return "bg-base-700 text-slate-300"
	case model.TypeBoolean:
		return "bg-accent-violet/15 text-accent-violet"
	case model.TypeDate, model.TypeTimestamp, model.TypeTime:
		return "bg-accent-amber/15 text-accent-amber"
	case model.TypeUUID:
		return "bg-accent-emerald/15 text-accent-emerald"
	case model.TypeStruct, model.TypeArray, model.TypeMap, model.TypeJSON:
		return "bg-accent-rose/15 text-accent-rose"
	default:
		return "bg-base-700 text-slate-300"
	}
}

// statusBadge returns classes for a run/job status badge.
func statusBadge(s model.RunStatus) string {
	switch s {
	case model.StatusCompleted:
		return "bg-accent-emerald/15 text-accent-emerald"
	case model.StatusRunning:
		return "bg-brand-500/15 text-brand-300"
	case model.StatusQueued:
		return "bg-accent-amber/15 text-accent-amber"
	case model.StatusFailed:
		return "bg-accent-rose/15 text-accent-rose"
	default:
		return "bg-base-700 text-slate-300"
	}
}

// qualityColor returns a text color class for a 0..100 quality score.
func qualityColor(score float64) string {
	switch {
	case score >= 80:
		return "text-accent-emerald"
	case score >= 60:
		return "text-accent-amber"
	default:
		return "text-accent-rose"
	}
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func pct100(f float64) string { return fmt.Sprintf("%.0f", f) }

func progressPct(f float64) string {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return fmt.Sprintf("%.0f%%", f*100)
}

func join(ss []string, sep string) string { return strings.Join(ss, sep) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
