package goserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"videoviewer/pkg/av"
)

// 下载队列：把 pkg/av 的同步整片下载包装为异步任务。任务状态机：
// queued（排队）→ running（下载中）→ done / failed / canceled，另有
// paused（已暂停）：中断时保留 .part 断点，继续时从断点续传。
// 调度器按 maxConcurrent 并行（默认 1，可在设置中调整），限速对
// 每个任务的分片下载生效；元数据落盘，重启后历史仍可见（进行中的
// 任务标记为中断，由用户手动重试，避免重启后自动跑流量；已暂停的
// 任务保持暂停）。

type downloadStatus string

const (
	dlStatusQueued   downloadStatus = "queued"
	dlStatusRunning  downloadStatus = "running"
	dlStatusDone     downloadStatus = "done"
	dlStatusFailed   downloadStatus = "failed"
	dlStatusCanceled downloadStatus = "canceled"
	dlStatusPaused   downloadStatus = "paused"
)

// isOccupying 判断状态是否正在占用目标文件（同 code+dir 不允许并发写；
// 暂停中的任务保留断点与目标文件，同样视为占用）。
func isOccupying(s downloadStatus) bool {
	return s == dlStatusQueued || s == dlStatusRunning || s == dlStatusPaused
}

// interruptStatus 返回被中断任务的终态：Pause 中断落 paused（断点保留，
// 可继续续传），Cancel 中断落 canceled。
func interruptStatus(t *downloadTask) downloadStatus {
	if t.paused {
		return dlStatusPaused
	}
	return dlStatusCanceled
}

// downloadTask 是一条下载任务。除 cancel 外全部字段由 downloadManager.mu
// 保护（Progress 回调来自下载 goroutine，同样在锁内更新）。
type downloadTask struct {
	ID            string         `json:"id"`
	Code          string         `json:"code"`
	Title         string         `json:"title,omitempty"`
	Cover         string         `json:"cover,omitempty"`
	Source        string         `json:"source,omitempty"`
	Variant       string         `json:"variant,omitempty"`
	QualityHeight int            `json:"quality_height,omitempty"`
	Dir           string         `json:"dir,omitempty"`
	Status        downloadStatus `json:"status"`
	Done          int            `json:"done_segments"`
	Total         int            `json:"total_segments"`
	Size          int64          `json:"size,omitempty"`
	FilePath      string         `json:"file_path,omitempty"`
	Error         string         `json:"error,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`

	cancel context.CancelFunc `json:"-"`
	done   chan struct{}      `json:"-"` // 下载 goroutine 收尾信号（未启动时为 nil）
	paused bool               `json:"-"` // 中断后期望终态为 paused（Pause 置位）
}

// downloadManager 管理下载队列：并发调度、进度跟踪与元数据落盘。
type downloadManager struct {
	srv *Server

	mu            sync.Mutex
	tasks         []*downloadTask // 按创建顺序（FIFO）
	maxConcurrent int
	speedLimit    int64 // 字节/秒，<=0 表示不限制
	running       int
}

func newDownloadManager(srv *Server) *downloadManager {
	m := &downloadManager{srv: srv, maxConcurrent: 1}
	m.loadTasks()
	return m
}

// tasksFile 返回任务元数据的落盘路径（随应用数据目录，桌面/Android 均可用）。
func tasksFile() (string, error) {
	dir, err := storageDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "download_tasks.json"), nil
}

// loadTasks 恢复上次的任务列表；进行中的任务统一标记为失败（进程重启
// 会中断分片下载，文件可能残缺），用户可手动重试。已暂停的任务保持
// 暂停（断点文件仍在，继续时续传）。
func (m *downloadManager) loadTasks() {
	path, err := tasksFile()
	if err != nil {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return // 首次运行还没有落盘文件
	}
	var tasks []*downloadTask
	if err := json.Unmarshal(raw, &tasks); err != nil {
		log.Printf("goserver: load download tasks: %v", err)
		return
	}
	for _, t := range tasks {
		if t.Status == dlStatusQueued || t.Status == dlStatusRunning {
			t.Status = dlStatusFailed
			t.Error = "应用重启导致下载中断，可重试"
		}
	}
	m.tasks = tasks
}

// saveLocked 原子落盘任务元数据（临时文件 + rename）。调用方需持有 m.mu。
func (m *downloadManager) saveLocked() {
	path, err := tasksFile()
	if err != nil {
		return
	}
	raw, err := json.MarshalIndent(m.tasks, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("goserver: save download tasks: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("goserver: save download tasks: %v", err)
	}
}

// Add 创建并入队一个下载任务，随后触发调度。同一目标文件（番号 + 目录）
// 已有排队中/下载中的任务时返回错误：并发写同一文件会产生错乱数据。
func (m *downloadManager) Add(t *downloadTask) (*downloadTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.tasks {
		if e.Code == t.Code && e.Dir == t.Dir && isOccupying(e.Status) {
			return nil, fmt.Errorf("a task for %s is already queued, paused or downloading", t.Code)
		}
	}
	if t.ID == "" {
		t.ID = newDownloadTaskID()
	}
	t.Status = dlStatusQueued
	t.CreatedAt = time.Now()
	t.UpdatedAt = t.CreatedAt
	m.tasks = append(m.tasks, t)
	m.saveLocked()
	m.scheduleLocked()
	return t, nil
}

// Snapshot 返回任务列表副本（最新的在前），用于序列化下发。
func (m *downloadManager) Snapshot() []downloadTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]downloadTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, *t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Get 返回任务副本；不存在时返回 nil。
func (m *downloadManager) Get(id string) *downloadTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.ID == id {
			cp := *t
			return &cp
		}
	}
	return nil
}

// Cancel 取消任务：排队中/已暂停的直接标记取消；下载中的中断其 context，
// 状态由 run 收尾时落为 canceled。取消优先于暂停（清除暂停标记）。
func (m *downloadManager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.ID != id {
			continue
		}
		switch t.Status {
		case dlStatusQueued, dlStatusPaused:
			t.paused = false
			t.Status = dlStatusCanceled
			t.UpdatedAt = time.Now()
			m.saveLocked()
		case dlStatusRunning:
			t.paused = false
			if t.cancel != nil {
				t.cancel()
			}
		}
		return true
	}
	return false
}

// Pause 暂停任务：排队中的直接置为暂停；下载中的中断其 context（断点文件
// 由 pkg/av 保留），状态由 run 收尾时落为 paused。已暂停的任务重复调用
// 幂等成功；返回 false 表示任务不存在或当前状态不可暂停。
func (m *downloadManager) Pause(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.ID != id {
			continue
		}
		switch t.Status {
		case dlStatusQueued:
			t.Status = dlStatusPaused
			t.UpdatedAt = time.Now()
			m.saveLocked()
			return true
		case dlStatusRunning:
			t.paused = true
			if t.cancel != nil {
				t.cancel()
			}
			return true
		case dlStatusPaused:
			return true
		}
		return false
	}
	return false
}

// Remove 从列表移除任务；deleteFile 为 true 时同时删除已下载文件。
// 正在下载的任务先被取消，等待其收尾（会落盘断点元数据）后再清理文件，
// 避免与收尾写盘竞争导致伴生文件残留。
func (m *downloadManager) Remove(id string, deleteFile bool) bool {
	m.mu.Lock()
	idx := -1
	for i, t := range m.tasks {
		if t.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return false
	}
	t := m.tasks[idx]
	if t.Status == dlStatusRunning && t.cancel != nil {
		t.cancel()
	}
	m.tasks = append(m.tasks[:idx], m.tasks[idx+1:]...)
	done := t.done
	m.mu.Unlock()

	// 等下载 goroutine 完全退出：其收尾会 flush 断点元数据，先删文件
	// 会被重新写入。30 秒上限防御异常挂起。
	if done != nil {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			log.Printf("goserver: remove %s: download goroutine did not finish in time", id)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if deleteFile {
		m.removeFilesLocked(t)
	}
	m.saveLocked()
	return true
}

// removeFilesLocked 删除任务关联的文件：完成品（FilePath，可能已转封装
// 为 .mp4）与 pkg/av 约定的三件套（{code}.ts 及其 .part/.part.meta
// 伴生文件）。进行中/失败的任务 FilePath 为空，按任务目录与番号推算
// 基路径，保证半成品同样被清理。调用方需持有 m.mu。
func (m *downloadManager) removeFilesLocked(t *downloadTask) {
	dir := t.Dir
	if dir == "" {
		dir = m.srv.cfg.DownloadDir
	}
	var paths []string
	if t.FilePath != "" {
		paths = append(paths, t.FilePath)
	}
	if dir != "" && t.Code != "" {
		base := filepath.Join(dir, t.Code+".ts")
		paths = append(paths, base, base+".part", base+".part.meta")
	}
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("goserver: remove download file: %v", err)
		}
	}
}

// Retry 把失败/已取消的任务重新排队。
func (m *downloadManager) Retry(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.ID != id {
			continue
		}
		if t.Status != dlStatusFailed && t.Status != dlStatusCanceled {
			return false
		}
		m.requeueLocked(t)
		return true
	}
	return false
}

// Resume 继续已暂停的任务：重新排队，实际下载时从 .part 断点续传。
func (m *downloadManager) Resume(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.ID != id {
			continue
		}
		if t.Status != dlStatusPaused {
			return false
		}
		m.requeueLocked(t)
		return true
	}
	return false
}

// requeueLocked 把任务重置为排队状态并触发调度（Retry/Resume 共用）。
// 调用方需持有 m.mu。
func (m *downloadManager) requeueLocked(t *downloadTask) {
	t.paused = false
	t.Status = dlStatusQueued
	t.Error = ""
	t.Done = 0
	t.Total = 0
	t.UpdatedAt = time.Now()
	m.saveLocked()
	m.scheduleLocked()
}

// SetConfig 更新调度配置（并发上限 / 限速）；并发上限提高时立即补位调度。
func (m *downloadManager) SetConfig(maxConcurrent int, speedLimitBytesPerSec int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxConcurrent > 0 {
		m.maxConcurrent = maxConcurrent
	}
	m.speedLimit = speedLimitBytesPerSec
	m.scheduleLocked()
}

// scheduleLocked 按并发上限从队首补位启动任务。调用方需持有 m.mu。
func (m *downloadManager) scheduleLocked() {
	for _, t := range m.tasks {
		if m.running >= m.maxConcurrent {
			return
		}
		if t.Status != dlStatusQueued {
			continue
		}
		t.Status = dlStatusRunning
		t.UpdatedAt = time.Now()
		m.running++
		m.saveLocked()
		go m.run(t)
	}
}

// run 执行一个任务的下载：解析（可选变体）→ 分片下载（限速）→ 合并落盘。
// 进度经 Progress 回调实时更新到任务对象，前端轮询获取。
func (m *downloadManager) run(t *downloadTask) {
	m.mu.Lock()
	speedLimit := m.speedLimit
	dir := t.Dir
	if dir == "" {
		dir = m.srv.cfg.DownloadDir
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	done := make(chan struct{})
	t.done = done
	m.mu.Unlock()
	defer close(done)

	opt := av.DownloadOptions{
		Source:           t.Source,
		Variant:          t.Variant,
		MaxQualityHeight: t.QualityHeight,
		Concurrency:      8,
		Progress: func(done, total int) {
			m.mu.Lock()
			t.Done = done
			t.Total = total
			m.mu.Unlock()
		},
	}
	if speedLimit > 0 {
		opt.SpeedLimitBytesPerSec = speedLimit
	}

	res, err := m.srv.av.Download(ctx, t.Code, dir, opt)
	canceled := ctx.Err() != nil
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.running--
	t.cancel = nil
	t.UpdatedAt = time.Now()
	switch {
	case err == nil:
		t.Status = dlStatusDone
		t.FilePath = res.FilePath
		t.Size = res.Size
		t.Total = res.Segments
		t.Done = res.Segments
		t.Error = ""
	case canceled:
		t.Status = interruptStatus(t)
		t.paused = false
	default:
		t.Status = dlStatusFailed
		t.Error = err.Error()
	}
	m.saveLocked()
	m.scheduleLocked()
}

// newDownloadTaskID 生成任务 ID（dl + 8 字节随机十六进制）。
func newDownloadTaskID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("dl%d", time.Now().UnixNano())
	}
	return "dl" + hex.EncodeToString(b[:])
}

// ---------- HTTP handlers ----------

// handleDownloadCreate 创建下载任务：POST /api/downloads
// body: {code, title, cover, source, variant, quality_height, dir}
func (s *Server) handleDownloadCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code          string `json:"code"`
		Title         string `json:"title"`
		Cover         string `json:"cover"`
		Source        string `json:"source"`
		Variant       string `json:"variant"`
		QualityHeight int    `json:"quality_height"`
		Dir           string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Code = strings.ToUpper(strings.TrimSpace(req.Code))
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "video code required")
		return
	}
	if req.Dir == "" {
		req.Dir = s.cfg.DownloadDir
	}
	if req.Dir == "" {
		writeError(w, http.StatusBadRequest, "download directory not configured")
		return
	}
	if !filepath.IsAbs(req.Dir) {
		writeError(w, http.StatusBadRequest, "download directory must be an absolute path")
		return
	}

	task, err := s.dl.Add(&downloadTask{
		Code:          req.Code,
		Title:         req.Title,
		Cover:         req.Cover,
		Source:        req.Source,
		Variant:       req.Variant,
		QualityHeight: req.QualityHeight,
		Dir:           req.Dir,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

// handleDownloadList 列出全部任务（最新在前）：GET /api/downloads
func (s *Server) handleDownloadList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":        s.dl.Snapshot(),
		"download_dir": s.cfg.DownloadDir,
	})
}

// handleDownloadGet 查询单个任务：GET /api/downloads/{id}
func (s *Server) handleDownloadGet(w http.ResponseWriter, r *http.Request) {
	t := s.dl.Get(r.PathValue("id"))
	if t == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": t})
}

// handleDownloadCancel 取消任务：POST /api/downloads/{id}/cancel
func (s *Server) handleDownloadCancel(w http.ResponseWriter, r *http.Request) {
	if !s.dl.Cancel(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownloadPause 暂停任务（排队中/下载中）：POST /api/downloads/{id}/pause
func (s *Server) handleDownloadPause(w http.ResponseWriter, r *http.Request) {
	if !s.dl.Pause(r.PathValue("id")) {
		writeError(w, http.StatusBadRequest, "task not pausable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownloadResume 继续已暂停的任务：POST /api/downloads/{id}/resume
func (s *Server) handleDownloadResume(w http.ResponseWriter, r *http.Request) {
	if !s.dl.Resume(r.PathValue("id")) {
		writeError(w, http.StatusBadRequest, "task not resumable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownloadRetry 重试失败/已取消的任务：POST /api/downloads/{id}/retry
func (s *Server) handleDownloadRetry(w http.ResponseWriter, r *http.Request) {
	if !s.dl.Retry(r.PathValue("id")) {
		writeError(w, http.StatusBadRequest, "task not retryable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownloadDelete 删除任务（可选删除文件）：
// DELETE /api/downloads/{id}?delete_file=1
func (s *Server) handleDownloadDelete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("delete_file")
	if !s.dl.Remove(r.PathValue("id"), q == "1" || q == "true") {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownloadConfig 更新调度配置：POST /api/downloads/config
// body: {max_concurrent, speed_limit_mbps}（speed_limit_mbps=0 表示不限速）
func (s *Server) handleDownloadConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxConcurrent  int `json:"max_concurrent"`
		SpeedLimitMBps int `json:"speed_limit_mbps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	s.dl.SetConfig(req.MaxConcurrent, int64(req.SpeedLimitMBps)*1024*1024)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownloadFile 流式输出已下载文件（支持 Range，供播放器 seek）：
// GET /api/downloads/{id}/file
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	t := s.dl.Get(r.PathValue("id"))
	if t == nil || t.Status != dlStatusDone || t.FilePath == "" {
		writeError(w, http.StatusNotFound, "file not ready")
		return
	}
	f, err := os.Open(t.FilePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found: "+err.Error())
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// MPEG-TS 容器（m3u8 分片合并产物），ExoPlayer/media_kit 均可直接播放。
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeContent(w, r, filepath.Base(t.FilePath), fi.ModTime(), f)
}
