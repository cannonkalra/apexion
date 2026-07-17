package selection

import (
	"context"
	"sync"
	"time"

	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/selection/compat"
)

// formatGroup is the compatibility grouping for one file format within a
// selection. Stored on the session so reader-option changes can regenerate SQL
// (sqlgen) without re-running schema discovery.
type formatGroup struct {
	Format model.Format
	Group  compat.Grouping
}

// Session is one user's live multi-file selection: the files, their discovery
// state, the grouping result, and the current reader options. All access goes
// through mu. A session is replaced wholesale (new struct, old context
// cancelled) whenever the selection changes, so background goroutines writing to
// an orphaned session are harmless.
type Session struct {
	mu        sync.Mutex
	token     string
	files     []*FileState
	groups    []formatGroup
	opts      model.ReadOptions
	summary   *CompatSummary
	complete  bool
	totalSize int64
	rowsEst   int64 // -1 until/unless computed
	cancel    context.CancelFunc
	createdAt time.Time
	touchedAt time.Time
}

// snapshot returns an immutable copy of the session state for the UI.
func (s *Session) snapshot() ProgressSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := ProgressSnapshot{
		Token:    s.token,
		Total:    len(s.files),
		Complete: s.complete,
		Summary:  s.summary,
		Files:    make([]FileState, len(s.files)),
	}
	for i, f := range s.files {
		snap.Files[i] = *f
		switch f.Status {
		case StatusDone:
			snap.Done++
		case StatusError:
			snap.Done++
			snap.Errored++
		}
	}
	if snap.Total > 0 {
		snap.Fraction = float64(snap.Done) / float64(snap.Total)
	}
	return snap
}

// sessionStore is an in-memory, TTL'd, size-bounded map of sessions.
type sessionStore struct {
	mu  sync.Mutex
	m   map[string]*Session
	ttl time.Duration
	max int
}

func newSessionStore(ttl time.Duration, max int) *sessionStore {
	return &sessionStore{m: map[string]*Session{}, ttl: ttl, max: max}
}

func (st *sessionStore) get(token string) (*Session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.m[token]
	if ok {
		s.mu.Lock()
		s.touchedAt = time.Now()
		s.mu.Unlock()
	}
	return s, ok
}

// put installs a session, cancelling and replacing any prior one under the same
// token, and evicts the oldest session if the store is over capacity.
func (st *sessionStore) put(s *Session) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if old, ok := st.m[s.token]; ok && old.cancel != nil {
		old.cancel()
	}
	st.m[s.token] = s
	if len(st.m) > st.max {
		st.evictOldestLocked()
	}
}

func (st *sessionStore) drop(token string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if s, ok := st.m[token]; ok {
		if s.cancel != nil {
			s.cancel()
		}
		delete(st.m, token)
	}
}

// reap cancels and removes sessions idle longer than the TTL.
func (st *sessionStore) reap(now time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for token, s := range st.m {
		s.mu.Lock()
		idle := now.Sub(s.touchedAt)
		s.mu.Unlock()
		if idle > st.ttl {
			if s.cancel != nil {
				s.cancel()
			}
			delete(st.m, token)
		}
	}
}

func (st *sessionStore) closeAll() {
	st.mu.Lock()
	defer st.mu.Unlock()
	for token, s := range st.m {
		if s.cancel != nil {
			s.cancel()
		}
		delete(st.m, token)
	}
}

// evictOldestLocked removes the least-recently-touched session. Caller holds mu.
func (st *sessionStore) evictOldestLocked() {
	var oldestTok string
	var oldest time.Time
	for token, s := range st.m {
		s.mu.Lock()
		t := s.touchedAt
		s.mu.Unlock()
		if oldestTok == "" || t.Before(oldest) {
			oldestTok, oldest = token, t
		}
	}
	if oldestTok != "" {
		if s := st.m[oldestTok]; s.cancel != nil {
			s.cancel()
		}
		delete(st.m, oldestTok)
	}
}
