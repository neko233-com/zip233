package archive

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Progress describes one step of a long-running archive operation. Done and
// Total count archive entries (or source files when creating an archive).
// A Total of 0 means the final entry count is not known yet; a final update
// with the accurate Total is always emitted when the operation ends.
type Progress struct {
	// JobID identifies the archive operation that produced this update. It is
	// empty for the legacy global SetProgress path (CLI TUI and old callers).
	JobID   string `json:"job_id,omitempty"`
	Phase   string `json:"Phase"`
	Done    int64  `json:"Done"`
	Total   int64  `json:"Total"`
	Message string `json:"Message"`
}

// ProgressFunc receives progress updates. It may be called from the goroutine
// performing the archive operation, so implementations should hand off work
// quickly instead of blocking.
type ProgressFunc func(Progress)

var (
	progressMu sync.RWMutex
	progressFn ProgressFunc
)

// SetProgress registers fn as the global progress callback. Pass nil to
// disable reporting. Reporting is opt-in: with no callback the archive
// functions run exactly as before. The global slot is used by the CLI TUI and
// by legacy callers; concurrent desktop jobs should use WithProgress instead
// so they cannot overwrite each other's callback.
func SetProgress(fn ProgressFunc) {
	progressMu.Lock()
	progressFn = fn
	progressMu.Unlock()
}

// hasProgress reports whether a global callback is currently registered.
func hasProgress() bool {
	progressMu.RLock()
	defer progressMu.RUnlock()
	return progressFn != nil
}

// globalProgress returns the global callback, if any. It is invoked outside
// the lock so a slow callback cannot block SetProgress or deadlock a
// re-entrant call.
func globalProgress() ProgressFunc {
	progressMu.RLock()
	fn := progressFn
	progressMu.RUnlock()
	return fn
}

type progressCtxKey struct{}

type jobProgress struct {
	jobID string
	fn    ProgressFunc
}

// WithProgress returns a context that routes the progress of one archive
// operation to fn, tagging every update with jobID. CreateWithContext,
// TestWithContext and ExtractWithContext read this context; callers that use
// the legacy functions keep using the global SetProgress callback. With
// per-job callbacks, concurrent operations never compete for a single global
// slot, so progress always reaches the task that produced it.
func WithProgress(ctx context.Context, jobID string, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressCtxKey{}, &jobProgress{jobID: jobID, fn: fn})
}

func progressFromContext(ctx context.Context) (string, ProgressFunc) {
	if ctx == nil {
		return "", nil
	}
	if jp, ok := ctx.Value(progressCtxKey{}).(*jobProgress); ok && jp != nil {
		return jp.jobID, jp.fn
	}
	return "", nil
}

// Progress updates can be extremely frequent (one per archive entry, and ZIP
// readers with tens of thousands of entries are common). Delivering every one
// of them to the UI thread makes rendering the bottleneck. These limits keep
// the callback bounded while the forced terminal update preserves a complete
// and accurate final state for both the CLI and the desktop UI.
const (
	progressInterval = 16 * time.Millisecond
	progressEveryN   = 64
)

// progressEmitter coalesces progress updates for a single archive operation.
// It is safe for concurrent use because writers such as the 7z writer may
// open source files from several goroutines.
type progressEmitter struct {
	phase    string
	jobID    string
	fn       ProgressFunc // nil means use the global SetProgress callback
	total    atomic.Int64
	mu       sync.Mutex
	started  bool
	lastEmit time.Time
	lastDone int64
}

// newProgressEmitter starts an emitter. A total <= 0 marks the total as
// unknown; call setTotal before the terminal report in that case. When ctx
// carries a WithProgress callback it is used exclusively for this operation,
// keeping concurrent jobs isolated from one another.
func newProgressEmitter(ctx context.Context, phase string, total int64) *progressEmitter {
	jobID, fn := progressFromContext(ctx)
	e := &progressEmitter{phase: phase, jobID: jobID, fn: fn}
	e.total.Store(total)
	return e
}

// active reports whether this emitter has a callback to deliver to.
func (e *progressEmitter) active() bool { return e.fn != nil || hasProgress() }

func (e *progressEmitter) setTotal(total int64) { e.total.Store(total) }

// report forwards an update unless it is redundant. force bypasses the rate
// limit and must be used for the terminal update of every operation.
func (e *progressEmitter) report(done int64, message string, force bool) {
	fn := e.fn
	if fn == nil {
		fn = globalProgress()
	}
	if fn == nil {
		return
	}
	now := time.Now()
	e.mu.Lock()
	if !force && e.started && done >= e.lastDone &&
		now.Sub(e.lastEmit) < progressInterval &&
		done-e.lastDone < progressEveryN {
		e.mu.Unlock()
		return
	}
	e.started = true
	e.lastEmit = now
	e.lastDone = done
	total := e.total.Load()
	e.mu.Unlock()
	fn(Progress{JobID: e.jobID, Phase: e.phase, Done: done, Total: total, Message: message})
}

// finish publishes the accurate total and a forced terminal update.
func (e *progressEmitter) finish(done int64, message string) {
	e.setTotal(done)
	e.report(done, message, true)
}
