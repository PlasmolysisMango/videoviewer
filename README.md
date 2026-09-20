# VideoViewer — JavDB 跨平台客户端（Go 后端 + Flutter 前端）

一个视频搜索 / 榜单 / 详情 / 磁链 / 订阅 / 播放 / 下载应用，覆盖 **Android、Windows、Web**：

- **Go 后端**（`cmd/javdbserver`）：本地 HTTP 服务，聚合 JavDB 数据与 AV 播放源，Android 上以
  gomobile AAR 内嵌进应用进程并以前台服务保活
- **Flutter 前端**（`flutter_app`）：Material 3 界面，移动端与桌面端共用一套代码
- **两个可复用 Go 库**：[`pkg/javdb`](#5-pkgjavdb--javdb-的-go-客户端库)（双后端客户端，可直接引用）、
  [`pkg/av`](#52-pkgav--视频播放与下载)（MissAV / Jable / HohoJ 播放与下载）
- **零业务依赖**：javdb 库只用标准库 + `goquery`；全部离线可测（`-race` 通过，不打真实网络）

---

## 1. 架构总览

```
┌──────────────────────┐      HTTP/JSON      ┌───────────────────────────┐
│    Flutter 前端      │ ◄─────────────────► │   Go HTTP Server          │
│  Android/Windows/Web │    127.0.0.1:18888  │   (cmd/javdbserver)       │
└──────────────────────┘                     └────────────┬──────────────┘
                                     ┌────────────────────┼─────────────────────┐
                                     ▼                    ▼                     ▼
                          ┌──────────────────┐  ┌──────────────────┐  ┌────────────────┐
                          │    pkg/javdb     │  │     pkg/av       │  │   下载目录     │
                          │  榜单/搜索/磁链  │  │  MissAV/Jable/   │  │   (-dl-dir)    │
                          │  演员/评论/订阅  │  │  HohoJ 播放源    │  │                │
                          └────────┬─────────┘  └──────────────────┘  └────────────────┘
                                   │
                    api（移动端 JSON，优先） + web（HTML 镜像，兜底）
```

各平台的后端承载方式：

| 平台 | 后端形态 | 生命周期 |
|---|---|---|
| Android | gomobile AAR 内嵌于应用进程 | `GoServerService` 前台服务（`specialUse`）承载，START_STICKY 自动恢复；请求层另有连接失败自愈重启 |
| Windows | `javdbserver.exe` 独立进程 | `-parent` 看门狗：主应用退出后 server 自杀，不留残留进程 |
| Web | 不内嵌 | 前端直连已部署的 server（`flutter run -d chrome` 需自行保证后端可达） |

---

## 2. 功能一览

- **搜索**：关键词 / 番号 / 演员近似搜索（简体 → 繁体自动映射）、筛选与排序
- **详情**：影片信息、磁链列表（中字 / 无码破解优先排序）、相似推荐（三维度并行聚合）、
  评论按需分页（首屏一页 + 加载更多）
- **榜单**：热播榜、分类排行榜（HTML）、TOP250（需 App 登录）、演员榜（有码 / 无码 / 欧美 / 素人四类）
- **演员页**：档案 + 作品列表，排序透传上游服务端（最新 / 最旧 / 最高分 / 最多播放）
- **订阅**：合集 / 题材 / 演员，本地持久化，开机自动刷新
- **历史记录**：播放位置记忆，续播
- **AV 播放**：MissAV / Jable / HohoJ 三源级联；变体（无码 / 中字 / 普通）探测与优先级；
  全部源不可用时明确提示「无播放源」；Jable 的 Cloudflare 凭据可注入
- **下载**：按 `-dl-dir` 落盘
- **加载优化**：首页推荐池与影片详情 SWR 缓存、图片磁盘缓存、画廊翻页预加载、
  TOP250 切面失败冷却

---

## 3. 快速开始

### 3.1 后端（源码运行）

```bash
go build -o javdbserver ./cmd/javdbserver
./javdbserver -addr 127.0.0.1:18888 -proxy http://127.0.0.1:7890
```

### 3.2 前端（本地开发）

```bash
cd flutter_app
flutter pub get
flutter run -d windows   # 或 -d <android-device>
```

前端启动时会自动拉起（或连接）本机后端；Web 平台需自行保证后端可达。

### 3.3 安装包（GitHub Actions）

推送 / 手动触发 `Build Release` workflow 自动产出并发布：

- **Windows**：应用 + `javdbserver.exe` 打包成一个 zip（artifact `videoviewer-windows`）
- **Android**：arm64-v8a APK（gomobile AAR 内嵌，签名后随 Release 发布）

### 3.4 登录与凭据

| 凭据 | 解锁能力 | 获取方式 |
|---|---|---|
| App JWT | TOP250、个人列表、演员榜 | 应用内登录（`POST /api/login`），或 `JAVDB_TOKEN` |
| Web cookie | 分类排行榜、题材浏览等登录墙页面 | 应用内导入（`POST /api/web-cookie`），或 `JAVDB_COOKIE` |

两个值都是机密：别写日志、别提交。桌面端持久化在 `~/.videoviewer/`
（`session.json`、`subscriptions.json`），Android 上迁移到应用私有目录（scoped storage 不可写共享存储）。

---

## 4. 服务端配置与 API

`javdbserver` 参数：

| 参数 | 默认 | 说明 |
|---|---|---|
| `-addr` | `:18888` | HTTP 监听地址 |
| `-api-base` | JavDB App API 地址 | API base URL |
| `-token` | `$JAVDB_TOKEN` | App JWT（TOP250 / 个人列表） |
| `-cookie` | `$JAVDB_COOKIE` | Web 会话 cookie（登录墙页面） |
| `-dl-dir` | `$JAVDB_DL_DIR` | 下载目录（不设则禁用下载） |
| `-proxy` | `$HTTP_PROXY` | HTTP / SOCKS5 代理 |
| `-parent` | `0` | 父进程 PID，父进程退出后自杀（Windows 启动器使用） |

HTTP API 一览（全部 JSON）：

| 分组 | 端点 | 说明 |
|---|---|---|
| 通用 | `GET /health` | 健康检查 |
| 认证 | `POST /api/login`、`POST /api/web-cookie` | 登录 / 导入 web cookie |
| JavDB | `GET /api/search` | 搜索（`?q=&page=&limit=&category=&sort=`…） |
| | `GET /api/movie/:id`、`/api/magnets/:id`、`/api/reviews/:id?page=&sort=` | 详情 / 磁链 / 评论（按需分页） |
| | `GET /api/similar/:id` | 相似推荐（聚合） |
| | `GET /api/ranking/:kind` | 榜单：`playback` / `movies` / `top250` / `actors`（`?category=&period=&limit=`） |
| | `GET /api/actor/:id`、`/api/actor-movies/:id` | 演员档案 / 作品（服务端排序透传） |
| | `GET /api/series-movies/:id`、`/api/lists/search`、`/api/lists/:id` | 系列作品 / 收录列表 |
| | `GET /api/tags`、`/api/genre` | 标签分组 / 题材浏览（需 web cookie） |
| 订阅 | `GET|POST /api/subscriptions`、`DELETE /api/subscriptions/:kind/:id` | 订阅管理 |
| AV | `GET /api/av/sources`、`/api/av/search`、`/api/av/detail/:code` | 源列表 / 搜索 / 详情 |
| | `GET /api/av/probe/:code`、`/api/av/resolve/:code`、`/api/av/play/:code` | 变体探测 / 全流解析 / 最优流 |
| | `POST /api/av/download/:code`、`POST /api/av/cf-cookie` | 下载 / 注入 Cloudflare 凭据 |
| 媒体 | `GET /api/img`、`GET /api/hls/playlist`、`GET /api/hls/segment` | 图片与 HLS 代理（Web 播放用） |

---

## 5. `pkg/javdb` — JavDB 的 Go 客户端库

一套稳定的 Go 接口同时驱动 **JavDB 移动端 JSON API** 与 **javdb.com 及其镜像的 HTML**，
哪个能回答就用哪个。附带 `cmd/javdbcli` 演示程序，每条子命令对应一个 `Client` 方法，输出即文档。

移动端 JSON API（`https://jdforrepam.com/api`）返回干净 JSON、不吃 Cloudflare，代价是每个请求
都要带 `jdsignature` 头（300 秒有效）：`"{unix}.{clientID}.{md5(unix + salt)}"`。本项目实现为
导出的 [`Signature(time.Time)`](pkg/javdb/signature.go)，并在 TTL 内复用签发结果。

```
                        ┌──────────────────────────────┐
   Client.Search ──────▶│  能力接口探测 + 后端优先级     │
   Client.Ranking ─────▶│  call(backends, op)          │
   Client.Movie  ──────▶│  同类失败 → 单一哨兵错误      │
                        └───────────┬──────────────────┘
                     api（优先）      │      web（兜底）
              ┌───────────────────┐  │  ┌────────────────────┐
              │ /v2/search        │  │  │ javdb.com → 镜像×2 │
              │ /v1/rankings/*    │  │  │ goquery 解析       │
              │ /v4/movies/{id}   │  │  │ /search /rankings  │
              │ jdsignature       │  │  │ /actors /tags …    │
              └─────────┬─────────┘  │  └─────────┬──────────┘
                        └────────┬───┴────────────┘
                        transport（重试 / 退避 / 限速 / 故障转移 / 会话）
                        cache  （TTL + singleflight 去重）
```

**能力接口**（`source.go`）：`MovieSearcher`、`ActorSearcher`、`Ranker`、`Detailer`、
`MagnetLister`、`ReviewLister`、`ActorDetailer`、`ListPager`、`TagProvider`、`Authenticator`。
聚合层用类型断言决定某个后端能不能干这活；不支持就返回 `ErrUnsupported` **并被跳过**，
而不是假装返回一份错数据 —— 例如「分类排行榜」在 App 端根本不存在，`api` 明确拒绝，请求自动落到 HTML。

**错误语义**（`errors.go`）：`ErrAuthRequired`、`ErrNotFound`、`ErrChallenge`、`ErrRateLimited`、
`ErrMaintenance`、`ErrUnsupported`、`ErrEmptyResult`、`ErrNoSite`、`ErrInvalidQuery`。
所有后端都失败时返回 `*MultiError`；登录墙**不重试**，CF/429 **重试并换站**。

**缓存**（`cache.go`）：按查询语义生成 key，TTL 内命中即返回；同一 key 的并发请求
singleflight 合并为一次外呼；**只缓存成功结果**。

### 5.1 Go 用法示例

```go
ctx := context.Background()

c, err := javdb.New(
    javdb.WithRateLimit(1.5),          // 每个后端 1.5 req/s
    javdb.WithLocale("zh-TW"),
    javdb.WithCache(10*time.Minute),
)

// 搜索：一次 JSON 请求，含分页与总数
res, err := c.SearchMovies(ctx, "ssis",
    javdb.WithCategory(javdb.CategoryCensored),
    javdb.WithSort(javdb.SortNewest),
    javdb.WithSubtitle(),
    javdb.WithPage(1, 20),
)

// 榜单
hot, _   := c.Playback(ctx, javdb.PeriodWeekly, javdb.Page{Limit: 20})
board, _ := c.CategoryRanking(ctx, javdb.PeriodDaily, javdb.CategoryUncensored, javdb.Page{})
top, _   := c.Top250(ctx, javdb.Top250OfYear(2025), javdb.Page{Page: 1, Limit: 25}) // 需登录
actors, _:= c.ActorRanking(ctx, javdb.CategoryCensored, javdb.Page{Page: 2, Limit: 10})

// 详情 / 磁链：番号会自动解析成 id
d, _  := c.Movie(ctx, "SSIS-001")
best, _ := c.BestMagnet(ctx, "SSIS-001")   // 优先含字幕，其次体积最大

// 演员：给名字或 id 都行
a, _ := c.Actor(ctx, "楓花戀")
list, _ := c.ActorMovies(ctx, a.ID, javdb.Page{Limit: 30})

// 只想要某一侧后端
c.Web()   // *webBackend，HTML
c.API()   // *apiBackend，JSON
```

只关心错误分类时：

```go
switch {
case errors.Is(err, javdb.ErrAuthRequired): // 需要 -cookie / app JWT
case errors.Is(err, javdb.ErrRateLimited):  // 降速
case errors.Is(err, javdb.ErrNotFound),
     errors.Is(err, javdb.ErrEmptyResult):  // 换关键词
}
```

### 5.2 `pkg/av` — 视频播放与下载

- **MissAV**：主源。域回退链（`.ai` / `.com` / 数字镜像）应对 Cloudflare；slug 尾部数字补零归一；
  变体（无码 / 中字 / 普通）聚合并按优先级排序
- **Jable**：需有效 `cf_clearance`（`POST /api/av/cf-cookie` 注入后自动携带）
- **HohoJ**：`resolve` 直接给出 m3u8 / mp4 直链；番号索引为未补零形式，查找按
  「原样优先、补零兜底」两段搜索并做前导零等价匹配
- **级联**：`play` / `resolve` / `probe` 逐源尝试，某源失败自动切换下一个；
  指定 `?source=` 时只试该源；全部不可用返回明确错误（前端显示「无播放源」）

### 5.3 演示 CLI

```
javdbcli search     <keyword>   [-sub] [-scope actor] [-category censored] [-sort newest]
                                [-filter c|p|m|s|nowatched] [-recent] [-year 2025] [-tag 4:15]
javdbcli ranking    playback|movies|top250|actors|fanza   [-period weekly] [-year 2025]
javdbcli movie      <id|code>   [-magnets]
javdbcli magnets    <id|code>
javdbcli browse     -category|-code|-maker|-series|-publisher|-director|-actor|-tag
javdbcli actor      <name|id>   [-movies]
javdbcli tags       -category censored
javdbcli reviews    <id|code>   [-sort latest]
javdbcli login      -user a@b   [-pass-file -] [-verify]
```

- flag 可写在位置参数前后任意位置；`-v` 打印故障转移与重试决策，`-json` 输出结构体
- 凭据 flag 均有环境变量默认值：`-cookie`←`JAVDB_COOKIE`，`-token`←`JAVDB_TOKEN`，`-proxy`←`JAVDB_PROXY`
- 退出码分级：`0` 成功，`3` 未命中/空结果，`4` 需要登录，`1` 其他

---

## 6. 真实端点可用性（2026-09-14 探测）

以下结论均来自对线上 App API / 镜像的直连请求，已写进对应代码注释。

| 端点 | 结果 | 采用 |
|---|---|---|
| `GET /v2/search` | 200，`q/page/limit/type/movie_type/movie_filter_by/movie_sort_by/from_recent/year/month` 全部有效 | ✅ 搜索主路径 |
| `GET /v1/rankings/playback` | 200，**必须带 `filter_by`**；固定返回 61 行，`limit`/`page`/`type` 被忽略 | ✅ 热播榜 + 后端本地切窗 |
| `GET /v1/rankings/movies` | **404，不存在** | ❌ 分类榜改走 HTML |
| `GET /v1/rankings/actors` | 200，`type` 必填（缺失 `ParameterInvalid`），`type=0/1/2/3` 生效；固定 97 行，忽略 `limit`/`page` | ✅ 演員排行 + 本地切窗 |
| `GET /v1/movies/top` | `JWTVerificationError`（需 App 登录） | ✅ TOP250（登录后） |
| `GET /v4/movies/{id}` | 200 完整详情；`/v1/movies/{id}` 404 | ✅ 详情 |
| `GET /v1/movies/{id}/magnets`、`/reviews` | 200 | ✅ |
| `GET /v1/actors/{id}`、`GET /v1/actors?search=&type=` | 200 | ✅ 演员档案 / 演员搜索 |
| `GET /v1/tags` | 200，但 `type` **只吃数字**：`type=censored` → **500** | ✅ 标签分组 |
| `GET /v1/lists/related` | 200 | ✅ 相关收录列表 |
| `POST /v1/sessions` | 参数为 `username`/`password` + `device_uuid` 等（换成 `email`/`login` 会报 `參數不能爲空: username`） | ✅ App 登录 |
| 镜像 `/search`、`/rankings/*`、`/tags`… | 200 但内容是登录墙 | ⚠ 需 cookie |
| 镜像 `/users/sign_in` | **404**：镜像不提供登录入口 | ❌ 只能导入主站 cookie |

补充实测（2026-09-18）：`/v1/movies/tags` 支持服务端排序 `sort_by=release`（asc/desc 均生效）、
`score`（方向固定 desc）、`hit`；其余取值被忽略回默认。演员榜为**当前时期**口径的官方热门榜，
榜单行不带任何热度数字字段，本地只透传顺序。

图片 CDN 实测为 `https://tp.spfcas.com/rhe951l4q/…`，`FixImageURL` 会把任意镜像前缀归一到
`https://c0.jdbstatic.com/…`，避免把「抓取站点」和「图片站点」耦合在一起。

---

## 7. 测试与验证

```bash
gofmt -l .
go vet ./...
go test ./...            # 约 0.3s
go test ./pkg/javdb/ -race
cd flutter_app && flutter analyze
```

测试全部离线：`testsupport_test.go` 提供记录型 `stub`、`routerHandler` fixture 回放、
`apiBody`/`apiErrBody` 构造 `{"success":1|0,…}` 信封。覆盖点包括：双后端回退与聚合错误、
api 优先时不被 web 抢答、番号→id 解析、磁链分类排序、ID 与番号的启发式区分、登录墙识别、
缓存命中与并发去重、参数翻译与 `ErrUnsupported` 守卫。

---

## 8. 已知限制与合规

- JavDB 自 2024 年起对绝大多数列表页要求登录，镜像不提供登录入口，因此 **HTML 后端在生产环境基本必须有 cookie**；没有 cookie 时相关能力会返回 `ErrAuthRequired`（不会静默返回空表）。
- App 端点会随版本变动，`jdsignature` 的 salt/clientID 是逆向产物，可能失效；届时只需替换 `signature.go` 中两个常量。
- 榜单端点不支持服务端分页，`Page` 由后端本地切窗实现，因此 `MaxPage` 是「本次返回的整表」算出来的，不是服务端真值。
- App 的磁链 `size` 是 `.torrent` 元数据的体积（KB 级），HTML 页面显示的是影片总容量：`Magnet.SizeBytes` 只在同一 `Source` 内可比。
- `/v2/search?type=movie` 只回答 `movies` + `current_page`，没有总页数/总数，所以 `SearchResult.MaxPage`/`Total` 会是 `0`（表示「未上报」，不是「只有一页」）。
- 演员榜是 JavDB 官方当前时期热门榜，上游不回传热度指标数字，无法自证排序依据。
- Jable 播放依赖有效的 `cf_clearance` cookie，失效时该源自动跳过并级联到下一源。
- Android 后台可用性由前台服务保障；个别厂商 ROM 的极端省电策略仍可能延迟恢复，请求层自愈会在回到前台后自动拉起后端。
- 本仓库只做**读取与个人使用**：请遵守 JavDB / 各播放源的服务条款与所在地区法律。内容含成人影像元数据，请在合规场景使用。
