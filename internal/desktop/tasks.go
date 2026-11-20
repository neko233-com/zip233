package desktop

import "time"

// maxConcurrentTasks bounds how many archive operations run at the same time.
// Operations beyond this limit wait in a FIFO queue and start automatically
// as running tasks finish.
const maxConcurrentTasks = 4

type pendingTask struct {
	id      string
	message string
	start   func()
}

// submitTask starts fn immediately if a slot is free, otherwise it queues the
// task. The caller must have already inserted the TaskEntry (as taskQueued)
// into a.tasks. Must be called on the UI thread.
func (a *App) submitTask(id, message string, start func()) {
	a.taskMu.Lock()
	if a.runningTasks < maxConcurrentTasks {
		a.runningTasks++
		a.taskMu.Unlock()
		a.markTaskRunning(id, message)
		start()
		return
	}
	a.pendingTasks = append(a.pendingTasks, pendingTask{id: id, message: message, start: start})
	a.taskMu.Unlock()
	a.persist()
	a.invalidate()
}

// markTaskRunning promotes a freshly submitted task from queued to running.
func (a *App) markTaskRunning(id, message string) {
	for i := range a.tasks {
		if a.tasks[i].ID != id {
			continue
		}
		a.tasks[i].Status = taskRunning
		a.tasks[i].Message = message
		a.tasks[i].Started = time.Now()
		break
	}
	a.persist()
	a.invalidate()
}

// taskFinished releases a running slot. If another task is waiting, the slot
// is handed to it directly so the concurrency limit is preserved. Must be
// called on the UI thread after the finished task's result is applied.
func (a *App) taskFinished() {
	a.taskMu.Lock()
	if len(a.pendingTasks) == 0 {
		if a.runningTasks > 0 {
			a.runningTasks--
		}
		a.taskMu.Unlock()
		a.persist()
		return
	}
	next := a.pendingTasks[0]
	a.pendingTasks = a.pendingTasks[1:]
	a.taskMu.Unlock()

	// The running count is unchanged: one task ended and one takes its slot.
	a.markTaskRunning(next.id, next.message)
	next.start()
}

// discardPending drops queued tasks that no longer exist in a.tasks (for
// example after a user removes an archive). Must be called on the UI thread.
func (a *App) discardPending() {
	a.taskMu.Lock()
	kept := a.pendingTasks[:0]
	for _, p := range a.pendingTasks {
		if a.hasTask(p.id) {
			kept = append(kept, p)
		}
	}
	a.pendingTasks = kept
	a.taskMu.Unlock()
}

func (a *App) hasTask(id string) bool {
	for i := range a.tasks {
		if a.tasks[i].ID == id {
			return true
		}
	}
	return false
}
