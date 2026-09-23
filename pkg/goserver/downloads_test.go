package goserver

import "testing"

// newPauseTestManager 构造一个不会真正启动下载的队列管理器：maxConcurrent=0
// 时 scheduleLocked 不会拉起 goroutine；任务元数据落在测试临时目录。
func newPauseTestManager(t *testing.T) *downloadManager {
	t.Helper()
	SetDataDir(t.TempDir())
	return &downloadManager{srv: &Server{}, maxConcurrent: 0}
}

// TestPauseResumeStateMachine 覆盖暂停/继续的状态迁移与互斥：
// queued↔paused、暂停任务占用目标文件、状态不符时拒绝操作。
func TestPauseResumeStateMachine(t *testing.T) {
	m := newPauseTestManager(t)
	task := &downloadTask{ID: "dl1", Code: "SSIS-001", Status: dlStatusQueued}
	m.tasks = []*downloadTask{task}

	// queued → paused
	if !m.Pause("dl1") {
		t.Fatal("Pause(queued) = false, want true")
	}
	if task.Status != dlStatusPaused {
		t.Fatalf("status = %s, want paused", task.Status)
	}
	// 已暂停的任务再次 Pause：幂等成功
	if !m.Pause("dl1") {
		t.Fatal("Pause(paused) = false, want true (idempotent)")
	}
	// 暂停中的任务占用目标文件：同 code+dir 新建被拒
	if _, err := m.Add(&downloadTask{Code: "SSIS-001"}); err == nil {
		t.Fatal("Add while paused should conflict")
	}
	// 不同目录互不影响
	if _, err := m.Add(&downloadTask{Code: "SSIS-001", Dir: "/tmp/other"}); err != nil {
		t.Fatalf("Add with different dir should succeed, got %v", err)
	}
	// paused → queued（Resume）
	if !m.Resume("dl1") {
		t.Fatal("Resume(paused) = false, want true")
	}
	if task.Status != dlStatusQueued {
		t.Fatalf("status = %s, want queued", task.Status)
	}
	// 非暂停态不可 Resume
	if m.Resume("dl1") {
		t.Fatal("Resume(queued) = true, want false")
	}
	// 不存在的任务不可操作
	if m.Pause("missing") || m.Resume("missing") {
		t.Fatal("Pause/Resume on missing task should be false")
	}
	// 终态不可暂停
	task.Status = dlStatusDone
	if m.Pause("dl1") {
		t.Fatal("Pause(done) = true, want false")
	}
}

// TestPauseRunningSetsCancelIntent 暂停下载中的任务：置暂停标记并中断
// context，中断终态由 interruptStatus 判定为 paused（保留断点可续传）。
func TestPauseRunningSetsCancelIntent(t *testing.T) {
	m := newPauseTestManager(t)
	canceled := false
	task := &downloadTask{ID: "dl1", Code: "C", Status: dlStatusRunning}
	task.cancel = func() { canceled = true }
	m.tasks = []*downloadTask{task}

	if !m.Pause("dl1") {
		t.Fatal("Pause(running) = false, want true")
	}
	if !canceled {
		t.Fatal("Pause(running) did not interrupt the download context")
	}
	if !task.paused {
		t.Fatal("pause intent flag not set on running task")
	}
	if got := interruptStatus(task); got != dlStatusPaused {
		t.Fatalf("interruptStatus = %s, want paused", got)
	}
}

// TestCancelOverridesPause 取消优先于暂停：暂停标记在途时取消会清除标记
// （收尾按取消处理）；已暂停的任务取消后可直接重试。
func TestCancelOverridesPause(t *testing.T) {
	m := newPauseTestManager(t)
	running := &downloadTask{ID: "dl1", Code: "C", Status: dlStatusRunning, paused: true}
	m.tasks = []*downloadTask{running}

	if !m.Cancel("dl1") {
		t.Fatal("Cancel(running) = false, want true")
	}
	if running.paused {
		t.Fatal("Cancel should clear the pause intent")
	}
	if got := interruptStatus(running); got != dlStatusCanceled {
		t.Fatalf("interruptStatus = %s, want canceled", got)
	}

	paused := &downloadTask{ID: "dl2", Code: "D", Status: dlStatusPaused}
	m.tasks = append(m.tasks, paused)
	if !m.Cancel("dl2") || paused.Status != dlStatusCanceled {
		t.Fatalf("Cancel(paused): status = %s, want canceled", paused.Status)
	}
	if !m.Retry("dl2") || paused.Status != dlStatusQueued {
		t.Fatalf("Retry(canceled): status = %s, want queued", paused.Status)
	}
}
