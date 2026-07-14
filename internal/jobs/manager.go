// Package jobs runs background work (crawls, inference) with persisted progress
// so the UI can render live progress bars and status.
package jobs

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/apexion/apexion/internal/model"
	"github.com/apexion/apexion/internal/storage"
)

// RunFunc is the body of a job. It receives a progress callback (fraction in
// 0..1 and a human message) and the job's context.
type RunFunc func(ctx context.Context, progress func(fraction float64, message string)) error

// Manager schedules and tracks background jobs with a bounded worker pool.
type Manager struct {
	store   *storage.Store
	log     zerolog.Logger
	sem     chan struct{}
	wg      sync.WaitGroup
	baseCtx context.Context
	cancel  context.CancelFunc

	mu      sync.Mutex
	cancels map[string]context.CancelFunc // jobID -> cancel
}

// NewManager creates a job manager with the given max concurrency.
func NewManager(store *storage.Store, log zerolog.Logger, concurrency int) *Manager {
	if concurrency <= 0 {
		concurrency = 4
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		store:   store,
		log:     log.With().Str("component", "jobs").Logger(),
		sem:     make(chan struct{}, concurrency),
		baseCtx: ctx,
		cancel:  cancel,
		cancels: map[string]context.CancelFunc{},
	}
}

// Cancel requests cancellation of a running or queued job. It returns false if
// the job is unknown or already finished.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	cancel, ok := m.cancels[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	m.log.Info().Str("job", id).Msg("job cancellation requested")
	return true
}

// Submit enqueues a job and returns its initial record. The job runs
// asynchronously on the manager's own context (it outlives the request).
func (m *Manager) Submit(jobType model.JobType, refID, label string, fn RunFunc) *model.Job {
	now := time.Now().UTC()
	job := &model.Job{
		ID: uuid.NewString(), Type: jobType, Status: model.StatusQueued,
		RefID: refID, Label: label, CreatedAt: now,
	}
	_ = m.store.SaveJob(m.baseCtx, job)

	jobCtx, cancel := context.WithCancel(m.baseCtx)
	m.mu.Lock()
	m.cancels[job.ID] = cancel
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.cancels, job.ID)
			m.mu.Unlock()
		}()
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
		case <-jobCtx.Done():
			return
		}
		m.run(jobCtx, job, fn)
	}()
	return job
}

func (m *Manager) run(ctx context.Context, job *model.Job, fn RunFunc) {
	start := time.Now().UTC()
	job.Status = model.StatusRunning
	job.StartedAt = &start
	job.Progress = 0
	_ = m.store.SaveJob(m.baseCtx, job)
	m.log.Info().Str("job", job.ID).Str("type", string(job.Type)).Str("label", job.Label).Msg("job started")

	// Throttle progress persistence to avoid hammering the store.
	var lastSave time.Time
	progress := func(fraction float64, message string) {
		job.Progress = clamp(fraction)
		job.Message = message
		if time.Since(lastSave) > 400*time.Millisecond || fraction >= 1 {
			lastSave = time.Now()
			_ = m.store.SaveJob(m.baseCtx, job)
		}
	}

	err := safeRun(ctx, fn, progress)
	end := time.Now().UTC()
	job.FinishedAt = &end
	switch {
	case ctx.Err() == context.Canceled && m.baseCtx.Err() == nil:
		// Cancelled via Cancel(id), not process shutdown.
		job.Status = model.StatusCancelled
		job.Message = "cancelled"
		m.log.Info().Str("job", job.ID).Msg("job cancelled")
	case err != nil:
		job.Status = model.StatusFailed
		job.Error = err.Error()
		m.log.Error().Err(err).Str("job", job.ID).Msg("job failed")
	default:
		job.Status = model.StatusCompleted
		job.Progress = 1
		job.Message = "completed"
		m.log.Info().Str("job", job.ID).Dur("took", end.Sub(start)).Msg("job completed")
	}
	_ = m.store.SaveJob(m.baseCtx, job)
}

func safeRun(ctx context.Context, fn RunFunc, progress func(float64, string)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &panicError{v: r}
		}
	}()
	return fn(ctx, progress)
}

type panicError struct{ v any }

func (e *panicError) Error() string { return "job panicked: " + toString(e.v) }

// Shutdown cancels running jobs and waits for them to finish (up to ctx).
func (m *Manager) Shutdown(ctx context.Context) {
	m.cancel()
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func clamp(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return "unknown panic"
}
