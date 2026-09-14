package av

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
)

// normalizeHost 归一化主机名：小写、去端口、去前导点。
// 使 "missav.ai:443"、"MissAV.AI"、".missav.ai" 归为同一键。
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	// 去端口（避开 IPv6 字面量中的冒号：仅当末段不含 ']' 时才视作 host:port）。
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	return strings.TrimPrefix(h, ".")
}

// lookupCF 按主机解析可用的 CF 凭证：动态 Provider 优先，其次静态 map；均须 Valid。
func (c *HTTPClient) lookupCF(rawHost string) (CFCredential, bool) {
	host := normalizeHost(rawHost)
	if c.cfProvider != nil {
		if cred, ok := c.cfProvider(host); ok && cred.Valid() {
			return cred, true
		}
	}
	if c.cf != nil {
		if cred, ok := c.cf[host]; ok && cred.Valid() {
			return cred, true
		}
	}
	return CFCredential{}, false
}

// CFStore 是 host→CFCredential 的线程安全存储，可选 JSON 文件持久化。
//
// 它有两个用途：
//  1. 作为 Options.CookieProvider 的数据源（store.Provider()）；
//  2. 作为「人工填入 / webview 抓取」的 cf_clearance 落点——一次写入、跨进程长期复用。
//
// cf_clearance 与出口 IP + UA 绑定，故写入时应连同其 UA 一起保存（CFCredential.UA），
// 回放时 HTTPClient 会自动锁定该 UA。换代理/换网络后需重新抓取。
type CFStore struct {
	mu   sync.RWMutex
	path string
	data map[string]CFCredential
}

// NewCFStore 构造存储；path 非空时尝试从该 JSON 文件加载已有凭证（文件不存在不报错）。
func NewCFStore(path string) *CFStore {
	s := &CFStore{path: path, data: map[string]CFCredential{}}
	if path != "" {
		_ = s.Load()
	}
	return s
}

// Set 写入（并规范化 host 键）；若配置了文件路径则立即持久化。
func (s *CFStore) Set(host string, cred CFCredential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[string]CFCredential{}
	}
	s.data[normalizeHost(host)] = cred
	if s.path != "" {
		_ = s.saveLocked()
	}
}

// Get 读取有效凭证；缺失或过期返回 ok=false。
func (s *CFStore) Get(host string) (CFCredential, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cred, ok := s.data[normalizeHost(host)]
	if !ok || !cred.Valid() {
		return CFCredential{}, false
	}
	return cred, true
}

// Delete 删除某主机凭证并同步持久化（用于收到 403/过期后清理）。
func (s *CFStore) Delete(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, normalizeHost(host))
	if s.path != "" {
		_ = s.saveLocked()
	}
}

// Provider 返回可赋给 Options.CookieProvider 的闭包。
func (s *CFStore) Provider() func(host string) (CFCredential, bool) {
	return s.Get
}

// Load 从 JSON 文件读入并覆盖内存数据。文件缺失返回 nil（视为空存储）。
func (s *CFStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	m := map[string]CFCredential{}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	normal := make(map[string]CFCredential, len(m))
	for h, cred := range m {
		normal[normalizeHost(h)] = cred
	}
	s.data = normal
	return nil
}

// Save 将当前数据写回文件（权限 0600，凭证敏感）。未配置路径则为空操作。
func (s *CFStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	return s.saveLocked()
}

func (s *CFStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
