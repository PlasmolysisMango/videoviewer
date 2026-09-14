package av

import (
	"context"
)

// Source 表示一个数据源（站点），需提供列表检索、详情与可播放流解析能力。
// 这是可插拔扩展的核心：新增站点只需实现本接口并在 Client 注册（见 Register）。
type Source interface {
	// Name 返回数据源标识，如 "missav"。
	Name() string

	// Search 按关键词检索视频列表。
	Search(ctx context.Context, q Query) ([]Video, error)

	// Latest 返回最新/最近更新的视频列表。
	Latest(ctx context.Context, q Query) ([]Video, error)

	// Detail 按番号获取视频完整元数据。
	Detail(ctx context.Context, code string) (*Video, error)

	// Resolve 按番号解析出所有可播放的 HLS 流（已解析为绝对地址并附带 Referer）。
	// 返回按带宽升序或任意顺序均可，Client 会自行挑选最优。
	Resolve(ctx context.Context, code string) ([]Stream, error)
}
