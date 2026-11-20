package desktop

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/neko233-com/zip233/internal/contract"
)

// TestApplyArchiveProgressByJobID checks that progress for concurrent tasks
// of the same kind is routed to the exact task, while legacy events without
// a JobID still fall back to the newest running task of that kind.
func TestApplyArchiveProgressByJobID(t *testing.T) {
	a := &App{tasks: []TaskEntry{
		{ID: "task-1", Kind: taskCreate, Status: taskRunning, Total: 10},
		{ID: "task-2", Kind: taskCreate, Status: taskRunning, Total: 10},
		{ID: "task-3", Kind: taskExtract, Status: taskRunning, Total: 10},
	}}

	a.applyArchiveProgress(contract.ArchiveProgress{
		JobID: "task-2", Phase: "create", Done: 5, Total: 10, Message: "精确匹配",
	})
	if a.tasks[0].Done != 0 || a.tasks[1].Done != 5 || a.tasks[1].Message != "精确匹配" {
		t.Fatalf("进度未按 JobID 归属：task-1=%+v task-2=%+v", a.tasks[0], a.tasks[1])
	}

	a.applyArchiveProgress(contract.ArchiveProgress{
		Phase: "create", Done: 3, Total: 10, Message: "旧消息",
	})
	if a.tasks[0].Done != 3 || a.tasks[1].Done != 5 {
		t.Fatalf("旧消息应按 kind 回退到最新运行任务：task-1=%+v task-2=%+v", a.tasks[0], a.tasks[1])
	}

	// 已结束任务迟到的进度既不能覆盖结果，也不能串台到别的任务。
	a.tasks[1].Status = taskDone
	a.applyArchiveProgress(contract.ArchiveProgress{
		JobID: "task-2", Phase: "create", Done: 9, Total: 10, Message: "迟到",
	})
	if a.tasks[0].Done != 3 || a.tasks[1].Done != 5 {
		t.Fatalf("迟到进度不应改写其他任务或已结束任务：task-1=%+v task-2=%+v", a.tasks[0], a.tasks[1])
	}
}

// TestQueueLimitAndPromotion verifies the concurrency cap and FIFO hand-off
// without touching MyGo rendering: only start callbacks are recorded.
func TestQueueLimitAndPromotion(t *testing.T) {
	a := &App{statePath: filepath.Join(t.TempDir(), "state.json")}
	const total = 6
	started := make([]string, 0, total)

	for i := 0; i < total; i++ {
		id := fmt.Sprintf("task-%d", i)
		a.tasks = append([]TaskEntry{{
			ID: id, Kind: taskCreate, Status: taskQueued, Message: "排队中…", Total: 1,
		}}, a.tasks...)
		captured := id
		a.submitTask(id, "运行中…", func() { started = append(started, captured) })
	}

	if len(started) != maxConcurrentTasks {
		t.Fatalf("应最多同时运行 %d 个任务，实际 %d", maxConcurrentTasks, len(started))
	}
	running, queued := 0, 0
	for _, task := range a.tasks {
		switch task.Status {
		case taskRunning:
			running++
		case taskQueued:
			queued++
		}
	}
	if running != maxConcurrentTasks || queued != total-maxConcurrentTasks {
		t.Fatalf("状态统计错误：running=%d queued=%d", running, queued)
	}

	// 两个任务结束，队首两个任务按提交顺序获得槽位。
	a.taskFinished()
	a.taskFinished()
	if len(started) != total {
		t.Fatalf("队列任务未自动启动：started=%v", started)
	}
	if started[maxConcurrentTasks] != "task-4" || started[maxConcurrentTasks+1] != "task-5" {
		t.Fatalf("队列未按 FIFO 启动：started=%v", started)
	}
	for _, id := range []string{"task-4", "task-5"} {
		found := false
		for _, task := range a.tasks {
			if task.ID == id && task.Status == taskRunning {
				found = true
			}
		}
		if !found {
			t.Errorf("队列任务 %s 未转为运行态", id)
		}
	}

	a.taskMu.Lock()
	count := a.runningTasks
	a.taskMu.Unlock()
	if count != maxConcurrentTasks {
		t.Errorf("并发计数应保持 %d，实际 %d", maxConcurrentTasks, count)
	}

	// 全部结束后释放槽位。
	for i := 0; i < maxConcurrentTasks; i++ {
		a.taskFinished()
	}
	a.taskMu.Lock()
	count = a.runningTasks
	a.taskMu.Unlock()
	if count != 0 {
		t.Errorf("全部结束后并发计数应为 0，实际 %d", count)
	}
}

// TestDiscardPendingSkipsRemovedTasks keeps the queue consistent when the
// user removes an archive while its task is still waiting.
func TestDiscardPendingSkipsRemovedTasks(t *testing.T) {
	a := &App{statePath: filepath.Join(t.TempDir(), "state.json")}
	for i := 0; i < maxConcurrentTasks+1; i++ {
		id := fmt.Sprintf("task-%d", i)
		a.tasks = append([]TaskEntry{{ID: id, Status: taskQueued}}, a.tasks...)
		a.submitTask(id, "运行中…", func() {})
	}
	// 最后一个提交的任务仍在队列中，移除后应从队列里清掉。
	for i := range a.tasks {
		if a.tasks[i].ID == "task-4" {
			a.tasks = append(a.tasks[:i], a.tasks[i+1:]...)
			break
		}
	}
	a.discardPending()
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	if len(a.pendingTasks) != 0 {
		t.Fatalf("被移除的任务不应留在队列：%v", a.pendingTasks)
	}
}
