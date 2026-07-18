package selection

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/apexion/apexion/internal/duckdb"
	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/selection/compat"
	"github.com/apexion/apexion/internal/selection/sqlgen"
)

const (
	sessionTTL    = 15 * time.Minute
	maxSessions   = 256
	discoveryConc = 6
	reapEvery     = 2 * time.Minute
)

// SelectionService owns selection sessions, runs background schema discovery,
// and produces the compatibility verdict and generated SQL. It is safe for
// concurrent use.
type SelectionService struct {
	desc        Describer
	reader      sqlgen.ReaderFunc
	compatOpts  compat.Options
	log         zerolog.Logger
	store       *sessionStore
	baseCtx     context.Context
	concurrency int
}

// New builds a SelectionService. desc is the DuckDB engine (satisfied by
// *duckdb.Engine); the balanced compatibility policy is used. A janitor
// goroutine reaps idle sessions until ctx is cancelled.
func New(ctx context.Context, desc Describer, log zerolog.Logger) *SelectionService {
	s := &SelectionService{
		desc:        desc,
		reader:      duckdb.ListReader,
		compatOpts:  compat.Options{}, // balanced default (bool+tinyint incompatible)
		log:         log.With().Str("component", "selection").Logger(),
		store:       newSessionStore(sessionTTL, maxSessions),
		baseCtx:     ctx,
		concurrency: discoveryConc,
	}
	go s.janitor(ctx)
	return s
}

// Set creates or replaces the selection for token (a new UUID when empty),
// cancelling any in-flight discovery, and starts fresh background discovery. It
// returns the token and an immediate pending snapshot.
func (s *SelectionService) Set(token string, files []FileRef) (string, ProgressSnapshot) {
	return s.setWith(token, files, defaultSelectionOpts())
}

// SetWith creates or replaces the selection with explicit reader options (from
// the Explorer's multi-file options strip), so background discovery reads the
// files the way the user asked from the first file — e.g. header=false.
func (s *SelectionService) SetWith(token string, files []FileRef, opts model.ReadOptions) (string, ProgressSnapshot) {
	return s.setWith(token, files, opts.Normalized())
}

// defaultSelectionOpts are the starting reader options for a multi-file
// selection: the safe defaults, but with the filename column off (a filename
// column on a combined virtual table is noise) so option forms round-trip
// consistently.
func defaultSelectionOpts() model.ReadOptions {
	o := model.DefaultReadOptions()
	o.Filename = false
	return o
}

// Reoptions re-runs discovery for an existing selection with new reader options
// (Header, UnionByName, IgnoreErrors) — the options affect how each file's
// schema is inferred, so a headerless CSV that mis-grouped under auto-detection
// can be fixed by turning the header off. Returns the token and a pending
// snapshot; discovery runs in the background as with Set.
func (s *SelectionService) Reoptions(token string, opts model.ReadOptions) (string, ProgressSnapshot, bool) {
	sess, ok := s.store.get(token)
	if !ok {
		return "", ProgressSnapshot{}, false
	}
	sess.mu.Lock()
	files := make([]FileRef, len(sess.files))
	for i, f := range sess.files {
		files[i] = f.Ref
	}
	sess.mu.Unlock()
	tok, snap := s.setWith(token, files, opts.Normalized())
	return tok, snap, true
}

// setWith creates or replaces the selection with the given files and reader
// options, cancelling any in-flight discovery and starting fresh.
func (s *SelectionService) setWith(token string, files []FileRef, opts model.ReadOptions) (string, ProgressSnapshot) {
	if token == "" {
		token = uuid.NewString()
	}
	now := time.Now()
	sess := &Session{
		token:     token,
		opts:      opts,
		rowsEst:   -1,
		createdAt: now,
		touchedAt: now,
	}
	for _, f := range files {
		sess.files = append(sess.files, &FileState{Ref: f, Status: StatusPending})
		sess.totalSize += f.Size
	}

	ctx, cancel := context.WithCancel(s.baseCtx)
	sess.cancel = cancel
	s.store.put(sess) // cancels & replaces any prior session for this token

	if len(files) == 0 {
		sess.mu.Lock()
		sess.complete = true
		sess.mu.Unlock()
		return token, sess.snapshot()
	}
	go s.discover(ctx, sess)
	return token, sess.snapshot()
}

// Progress returns the current snapshot for a token.
func (s *SelectionService) Progress(token string) (ProgressSnapshot, bool) {
	sess, ok := s.store.get(token)
	if !ok {
		return ProgressSnapshot{}, false
	}
	return sess.snapshot(), true
}

// Regenerate updates a completed selection's reader options and rebuilds the
// generated SQL (no re-discovery). Returns the refreshed summary.
func (s *SelectionService) Regenerate(token string, opts model.ReadOptions) (*CompatSummary, bool) {
	sess, ok := s.store.get(token)
	if !ok {
		return nil, false
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.opts = opts.Normalized()
	sess.summary = s.buildSummaryLocked(sess)
	return sess.summary, true
}

// RediscoverSync re-runs discovery synchronously with new reader options and
// returns the resulting summary. Used by the SQL options drawer, where the user
// waits for the regenerated schema + SQL; discovery is footer/sample only, so a
// brief block is acceptable. Returns nil if the token is gone or it times out.
func (s *SelectionService) RediscoverSync(token string, opts model.ReadOptions) (*CompatSummary, bool) {
	return s.RediscoverSyncAs(token, opts, "")
}

// RediscoverSyncAs is RediscoverSync with an optional format override: when
// formatOverride is a real format, every selected file is re-read (and grouped)
// as that format, so a mixed or misdetected selection can be coerced into one
// reader — e.g. all files as CSV with a chosen delimiter. An empty/unknown
// override keeps each file's detected format.
func (s *SelectionService) RediscoverSyncAs(token string, opts model.ReadOptions, formatOverride model.Format) (*CompatSummary, bool) {
	sess, ok := s.store.get(token)
	if !ok {
		return nil, false
	}
	sess.mu.Lock()
	files := make([]FileRef, len(sess.files))
	for i, f := range sess.files {
		files[i] = f.Ref
		if formatOverride != "" && formatOverride != model.FormatUnknown {
			files[i].Format = formatOverride
		}
	}
	sess.mu.Unlock()

	now := time.Now()
	fresh := &Session{token: token, opts: opts.Normalized(), rowsEst: -1, createdAt: now, touchedAt: now}
	for _, f := range files {
		fresh.files = append(fresh.files, &FileState{Ref: f, Status: StatusPending})
		fresh.totalSize += f.Size
	}
	ctx, cancel := context.WithTimeout(s.baseCtx, 45*time.Second)
	defer cancel()
	fresh.cancel = cancel
	s.store.put(fresh) // replaces + cancels any prior discovery for this token
	s.discover(ctx, fresh)
	return fresh.snapshot().Summary, true
}

// Drop discards a selection session.
func (s *SelectionService) Drop(token string) { s.store.drop(token) }

// Close cancels all sessions (called on app shutdown).
func (s *SelectionService) Close() { s.store.closeAll() }

// discover runs schema discovery for every file in the session in parallel
// (bounded), then groups and summarizes. It bails out silently if the context
// is cancelled (the session was replaced or the app is shutting down).
func (s *SelectionService) discover(ctx context.Context, sess *Session) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(s.concurrency)

	sess.mu.Lock()
	opts := sess.opts
	sess.mu.Unlock()

	for _, fsRef := range sess.files {
		fst := fsRef // capture
		g.Go(func() error {
			if gctx.Err() != nil {
				return nil
			}
			s.mark(sess, fst, StatusProcessing, nil, "")
			cols, err := s.desc.DescribeFile(gctx, fst.Ref.Bucket, fst.Ref.Key, fst.Ref.Format, opts)
			if gctx.Err() != nil {
				return nil // cancelled mid-flight; leave state, session is orphaned
			}
			if err != nil {
				s.mark(sess, fst, StatusError, nil, err.Error())
				return nil
			}
			s.mark(sess, fst, StatusDone, toColumns(cols), "")
			return nil
		})
	}
	_ = g.Wait()
	if ctx.Err() != nil {
		return
	}

	sess.mu.Lock()
	sess.groups = s.groupLocked(sess)
	sess.mu.Unlock()

	// Row estimate: exact only when every discovered file is Parquet (footer
	// metadata, no scan). Otherwise unknown.
	rows := s.parquetRows(ctx, sess)

	sess.mu.Lock()
	sess.rowsEst = rows
	sess.summary = s.buildSummaryLocked(sess)
	sess.complete = true
	sess.mu.Unlock()
}

// mark updates one file's discovery state under the session lock.
func (s *SelectionService) mark(sess *Session, fst *FileState, st Status, cols []compat.Column, errMsg string) {
	sess.mu.Lock()
	fst.Status = st
	if cols != nil {
		fst.Cols = cols
	}
	fst.Err = errMsg
	sess.mu.Unlock()
}

// groupLocked builds one compatibility grouping per file format from the files
// that discovered successfully. Caller holds sess.mu.
func (s *SelectionService) groupLocked(sess *Session) []formatGroup {
	byFormat := map[model.Format][]compat.FileSchema{}
	var order []model.Format
	for _, f := range sess.files {
		if f.Status != StatusDone {
			continue
		}
		if _, seen := byFormat[f.Ref.Format]; !seen {
			order = append(order, f.Ref.Format)
		}
		byFormat[f.Ref.Format] = append(byFormat[f.Ref.Format], compat.FileSchema{Ref: f.Ref, Cols: f.Cols})
	}
	groups := make([]formatGroup, 0, len(order))
	for _, format := range order {
		groups = append(groups, formatGroup{Format: format, Group: compat.Group(byFormat[format], s.compatOpts)})
	}
	return groups
}

// parquetRows sums the Parquet footer row counts across a session, but only when
// every discovered file is Parquet; otherwise returns -1 (unknown).
func (s *SelectionService) parquetRows(ctx context.Context, sess *Session) int64 {
	sess.mu.Lock()
	var uris []string
	allParquet := true
	for _, f := range sess.files {
		if f.Status != StatusDone {
			continue
		}
		if f.Ref.Format != model.FormatParquet {
			allParquet = false
			break
		}
		uris = append(uris, f.Ref.URI())
	}
	sess.mu.Unlock()
	if !allParquet || len(uris) == 0 {
		return -1
	}
	var total int64
	for _, uri := range uris {
		if ctx.Err() != nil {
			return -1
		}
		n, err := s.desc.ParquetRowCount(ctx, uri)
		if err != nil {
			return -1 // give up cleanly rather than show a partial count
		}
		total += n
	}
	return total
}

// buildSummaryLocked computes verdicts, counts, and generated SQL from the
// session's groups and current reader options. Caller holds sess.mu. It also
// writes each file's Verdict so snapshots and OOB badges reflect compatibility.
func (s *SelectionService) buildSummaryLocked(sess *Session) *CompatSummary {
	sum := &CompatSummary{RowsEst: sess.rowsEst, TotalSize: sess.totalSize, MixedFormat: len(sess.groups) > 1, Options: sess.opts}

	member := map[string]bool{}
	warn := map[string]bool{}
	reject := map[string]bool{}

	for _, fg := range sess.groups {
		gt, err := sqlgen.Build(fg.Group, fg.Format, sess.opts, s.reader)
		if err != nil {
			s.log.Warn().Err(err).Str("format", string(fg.Format)).Msg("sql generation failed")
		}
		sum.Tables = append(sum.Tables, gt)
		sum.Formats = append(sum.Formats, fg.Format)
		sum.Warnings = append(sum.Warnings, fg.Group.Warnings...)
		sum.Rejected = append(sum.Rejected, fg.Group.Rejected...)
		for _, m := range fg.Group.Members {
			member[refKey(m)] = true
		}
		for _, w := range fg.Group.Warnings {
			warn[refKey(w.File)] = true
		}
		for _, r := range fg.Group.Rejected {
			reject[refKey(r.File)] = true
		}
	}

	// Assign per-file verdicts and tally counts (distinct files).
	for _, f := range sess.files {
		k := refKey(f.Ref)
		switch {
		case f.Status == StatusError, reject[k]:
			f.Verdict = VerdictIncompatible
			sum.ErrorCount++
		case warn[k]:
			f.Verdict = VerdictWarning
			sum.WarnCount++
		case member[k]:
			f.Verdict = VerdictCompatible
			sum.CompatCount++
		default:
			f.Verdict = VerdictNone
		}
	}

	if pt := sum.PrimaryTable(); pt != nil {
		sum.Merged = pt.Merged
		sum.ColumnCount = len(pt.Merged)
	}
	return sum
}

// janitor reaps idle sessions until the base context is cancelled.
func (s *SelectionService) janitor(ctx context.Context) {
	t := time.NewTicker(reapEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.store.reap(now)
		}
	}
}

func refKey(r FileRef) string { return r.Bucket + " " + r.Key + " " + string(r.Format) }

func toColumns(cols []duckdb.ColumnDef) []compat.Column {
	out := make([]compat.Column, len(cols))
	for i, c := range cols {
		out[i] = compat.Column{Name: c.Name, Type: compat.Normalize(c.Type)}
	}
	return out
}
