package goserver

import (
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"

	"videoviewer/pkg/aacg"
)

// aacgTargetsMax 与 pkg/aacg 的发现结果上限一致（config 最多 32 条路由）。
const aacgTargetsMax = 32

// persistedAacgTargets 是镜像发现结果的跨重启缓存：发现链路（入口→转发→落地）
// 被网络阻断或持续瞬断时，上次成功校验过的镜像经 Check 复核后仍可续用。
type persistedAacgTargets struct {
	Targets []aacg.Target `json:"targets"`
}

// aacgTargetsFile returns the persisted mirror-list path (<data>/aacg_targets.json).
func aacgTargetsFile() (string, error) {
	dir, err := storageDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aacg_targets.json"), nil
}

// loadAacgTargets reads the persisted mirror list; a missing or corrupt file
// means no fallback (nil). Entries are only shape-checked here — usability is
// decided by Check at use time.
func loadAacgTargets() []aacg.Target {
	path, err := aacgTargetsFile()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var persisted persistedAacgTargets
	if err := json.Unmarshal(data, &persisted); err != nil {
		log.Printf("goserver: ignoring corrupt aacg targets cache: %v", err)
		return nil
	}
	var targets []aacg.Target
	for _, target := range persisted.Targets {
		if len(targets) >= aacgTargetsMax {
			break
		}
		u, err := url.Parse(target.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

// saveAacgTargets persists the mirror list atomically (temp file + rename,
// same as session.json). Failures are logged, never fatal: the in-memory
// list keeps working for this run.
func saveAacgTargets(targets []aacg.Target) {
	if len(targets) == 0 {
		return
	}
	path, err := aacgTargetsFile()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	if len(targets) > aacgTargetsMax {
		targets = targets[:aacgTargetsMax]
	}
	data, err := json.Marshal(persistedAacgTargets{Targets: targets})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("goserver: save aacg targets: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		log.Printf("goserver: save aacg targets: %v", err)
	}
}
