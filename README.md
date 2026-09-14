# javdb — JavDB 的 Go 客户端（搜索 / 榜单 / 详情 / 磁链 / 演员）

`pkg/javdb` 是一个可直接引用的库：一套稳定的 Go 接口，同时驱动 **JavDB 移动端 JSON API** 与
**javdb.com 及其镜像的 HTML**，哪个能回答就用哪个。附带 `cmd/javdbcli` 演示程序，
每条子命令对应一个 `Client` 方法，输出即文档。

- 零业务依赖：只用标准库 + `goquery`
- 全部离线可测：111 个测试、`-race` 通过，不打真实网络
- 真实端点已逐个探测并订正（见 [§4](#4-真实端点可用性2026-09-14-探测)）

---

## 1. 两个参考项目给了什么

### [TongWu/JAVDB_AutoSpider](https://github.com/TongWu/JAVDB_AutoSpider)

Python + requests 的定时抓取脚本：**必须先拿浏览器登录后的 `_janus_session_` cookie**，
再抓 javdb.com 的 HTML，解析卡片/详情页，把番号、标题、预览图、磁链入库。

| 值得继承 | 本项目做法 |
|---|---|
| 列表卡片字段抽取（番号/时长/评分/磁链数/"可播放""含字幕"标记） | `parse_list.go` 逐选择器实现，并有 fixture 测试 |
| 详情页 label/value 表 + 磁链表 | `parse_detail.go`，中英文 label 都映射到同一结构体 |
| 主站常被 Cloudflare 挡住，靠数字镜像域名 | `DefaultSites` 三个域 + `transport` 逐站点故障转移 |
| 需要限速，否则 429 / 封 IP | `WithRateLimit`（令牌桶，按后端独立计数） |
| **问题**：只有 HTML 一条腿；结构体散落 dict；无分页/错误语义；无缓存 | 见 §2 |

### [JavdBviewed/JavdBviewed](https://github.com/JavdBviewed/JavdBviewed)

浏览器扩展。除 DOM 注入外，最重要的发现是**移动端 JSON API**（`https://jdforrepam.com/api`）：
返回干净 JSON、不吃 Cloudflare，代价是每个请求都要带一个 `jdsignature` 头。

逆向出的签名方案（300 秒有效）：

```
jdsignature = "{unix}.{clientID}.{md5(unix + salt)}"
clientID    = lpw6vgqzsp
salt        = 71cf27bb…a199e7d5a…（128 位 hex，来自 app 二进制）
```

本项目实现为 [`Signature(time.Time)`](pkg/javdb/signature.go)（导出，便于外部自行签发），
并在 `signatureCache` 里按 TTL 复用，避免每次请求都重算。

| 值得继承 | 本项目做法 |
|---|---|
| `jdsignature` + `Dart/3.5 (dart:io)` UA | `transport.decorate`，`api` 后端全部请求自动附加 |
| JSON 端点清单（搜索/榜单/详情/磁链/评论/演员/标签/登录） | `api.go`，端点存在性已实测订正 |
| 图片 CDN 前缀（`…/rhe951l4q/…` 会随站点变化） | `FixImageURL` 统一归一到 `c0.jdbstatic.com` |
| **问题**：面向 UI 注入，没有数据模型；端点靠试，部分参数其实无效 | §4 给出实测结论 |

---

## 2. 实现结构

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
所有后端都失败时返回 `*MultiError`（实现 `Unwrap() []error`，`errors.Is` 可同时命中多个哨兵）；
失败方式一致时折叠成单个哨兵，日志更干净。页面级 hostile 响应（登录墙 / CF Interstitial / 维护页）
由 `transport.classify` 统一识别，登录墙**不重试**，CF/429 **重试并换站**。

**缓存**（`cache.go`）：按查询语义生成 key（搜索含 scope/分类/筛选/排序/年份/`FromRecent`，
榜单含 kind/period/category/filter/slice/start_rank/page/limit），TTL 内命中即返回；
同一 key 的并发请求 singleflight 合并为一次外呼；**只缓存成功结果**。

---

## 3. 快速开始

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
for _, m := range res.Movies {
    fmt.Println(m.Code, m.Title, m.ReleaseDate, m.Score, m.MagnetsCount)
}

// 榜单
hot, _   := c.Playback(ctx, javdb.PeriodWeekly, javdb.Page{Limit: 20})   // 热播榜
board, _ := c.CategoryRanking(ctx, javdb.PeriodDaily, javdb.CategoryUncensored, javdb.Page{}) // 分類排行榜（HTML）
top, _   := c.Top250(ctx, javdb.Top250OfYear(2025), javdb.Page{Page: 1, Limit: 25})           // 需登录
actors, _:= c.ActorRanking(ctx, javdb.CategoryCensored, javdb.Page{Page: 2, Limit: 10})       // 演員排行

// 详情 / 磁链：番号会自动解析成 id
d, _  := c.Movie(ctx, "SSIS-001")
best, _ := c.BestMagnet(ctx, "SSIS-001")   // 优先含字幕，其次体积最大
fmt.Println(d.URL(c.Site()), best.Name, best.Hash, best.SizeText)

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

---

## 4. 真实端点可用性（2026-09-14 探测）

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
| 镜像 `/search`、`/rankings/*`、`/tags`… | 200 但内容是登录墙（`<title> 登入 \| JavDB 成人影片數據庫 </title>`） | ⚠ 需 cookie |
| 镜像 `/users/sign_in` | **404**：镜像不提供登录入口 | ❌ 只能导入主站 cookie |
| `javdb.com` | 本沙箱三次 20s 超时 | — |

图片 CDN 实测为 `https://tp.spfcas.com/rhe951l4q/…`，`FixImageURL` 会把任意镜像前缀归一到
`https://c0.jdbstatic.com/…`，避免把「抓取站点」和「图片站点」耦合在一起。

---

## 5. 登录态：两条独立的路

| 数据 | 需要的凭据 | 取得方式 |
|---|---|---|
| App JSON（TOP250、个人列表） | App JWT | `Client.Login(ctx, Credentials{Username,Password})`，或导入 `WithAppToken` |
| HTML（分类排行榜、`browse`、标签、演员作品页） | 会话 cookie | 只能导入：`WithCookie("_janus_session_=…")` |

```bash
# App 侧：拿到 JWT（密码走 stdin，不进 shell history）
printf '%s' "$JAVDB_PASSWORD" | javdbcli login -user you@example.com -pass-file -
# 输出 export JAVDB_TOKEN='…'，随后可直接用
export JAVDB_TOKEN='…'
javdbcli ranking top250 -limit 20

# HTML 侧：浏览器已登录 → DevTools → Network → 任一文档请求 → 复制整行 cookie
#（注意 JavDB 的会话 cookie 是 HttpOnly，document.cookie 里看不到）
export JAVDB_COOKIE='_janus_session_=…; …'
javdbcli ranking movies -category uncensored -period daily
javdbcli browse -code SSIS
```

`Client.Login` 会遍历所有实现 `Authenticator` 的后端；`Session()` 可读回当前生效的
cookie / JWT（两个值都是机密，别写日志、别提交）。仓库已用 `.gitignore` 屏蔽
`account.txt`、`*.token`、`.env`。

---

## 6. 演示 CLI

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

标签筛选（`-tag 4:15`，即「主題=巨乳」）只在 `-category movies` 这一条路径上有落点：
App 的 `/v2/search` 没有 tag 参数，HTML 只支持站内分类列表，因此其它组合会被显式拒绝
（`ErrUnsupported`）而不是悄悄返回一份没过滤的结果。

- flag 可以写在位置参数**前后任意位置**（`parseArgs` 会重排），`-v` 打印故障转移与重试决策，`-json` 输出结构体
- 凭据 flag 均有环境变量默认值：`-cookie`←`JAVDB_COOKIE`，`-token`←`JAVDB_TOKEN`，`-proxy`←`JAVDB_PROXY`
- 退出码分级：`0` 成功，`3` 未命中/空结果，`4` 需要登录，`1` 其他；`explain()` 会为哨兵错误附上可执行建议

已实测通过的匿名冒烟：

```
$ javdbcli search ssis -limit 3                      # 经 api
$ javdbcli ranking playback -period weekly -limit 3   # 经 api，rank 1..3
$ javdbcli ranking actors -limit 5 -page 2            # 经 api，rank 6..10（后端本地切窗）
$ javdbcli movie SSIS-001 -magnets                    # 番号→id→详情+磁链
$ javdbcli tags -category censored                     # 经 api（type 必须是数字）
```

---

## 7. 测试与验证

```bash
gofmt -l .
go vet ./...
go test ./...            # 约 0.3s
go test ./pkg/javdb/ -race
```

测试全部离线：`testsupport_test.go` 提供记录型 `stub`（按 path 记录 method/query/header/body）、
`routerHandler` fixture 回放、`apiBody`/`apiErrBody` 构造 `{"success":1|0,…}` 信封。
覆盖点包括：双后端回退与聚合错误、api 优先时不被 web 抢答、番号→id 解析、磁链分类
（`[CNSub]`/`UC無碼破解` 等真实标记 → 字幕 > 无码破解 > 普通，再按体积）、
ID 与番号的启发式区分（`looksLikeID` vs `IsPlausibleVideoCode`）、登录墙识别
（真实标题带前导空格也要认出）、缓存命中与并发去重、参数翻译与 `ErrUnsupported` 守卫。

---

## 8. 已知限制与合规

- JavDB 自 2024 年起对绝大多数列表页要求登录，镜像不提供登录入口，因此 **HTML 后端在生产环境基本必须有 cookie**；没有 cookie 时相关能力会返回 `ErrAuthRequired`（不会静默返回空表）。
- App 端点会随版本变动，`jdsignature` 的 salt/clientID 是逆向产物，可能失效；届时只需替换 `signature.go` 中两个常量。
- 榜单端点不支持服务端分页，`Page` 由后端本地切窗实现，因此 `MaxPage` 是「本次返回的整表」算出来的，不是服务端真值。
- App 的磁链 `size` 是 `.torrent` 元数据的体积（KB 级），HTML 页面显示的是影片总容量：`Magnet.SizeBytes` 只在同一 `Source` 内可比，磁链排序也以名称标记（中字/无码破解）优先、体积仅用于同分拆位。
- `/v2/search?type=movie` 只回答 `movies` + `current_page`，没有总页数/总数，所以 `SearchResult.MaxPage`/`Total` 会是 `0`（表示「未上报」，不是「只有一页」）。
- 本仓库只做**读取**：不含下载、转码、分发。请遵守 JavDB 服务条款与所在地区法律，把 `WithRateLimit` 保持在对岸可接受的范围内（默认 2 req/s）。内容含成人影像元数据，请在合规场景使用。
