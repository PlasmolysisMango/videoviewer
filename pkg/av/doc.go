// Package av 提供一套统一的影视资源接口，支持搜索、播放（解析可播放流）与下载。
//
// 设计参考并整合了两个开源项目的核心能力：
//   - NASSAV (https://github.com/Satoing/NASSAV)：多数据源下载器，从站点 HTML 中提取 m3u8 并下载转码。
//   - missav-bot (https://github.com/Syngnat/missav-bot)：MissAV 爬虫，提供最新/搜索/演员/详情列表能力。
//
// 核心抽象：
//
//	Source —— 单个数据源（missav / jable / hohoj ...），实现 Search/Latest/Detail/Resolve。
//	Client —— 聚合多个 Source，按优先级调度，对外提供 Search/Latest/Detail/Play/Download。
//
// 典型用法：
//
//	c := av.NewClient()
//	videos, _ := c.Search(ctx, av.Query{Keyword: "SSIS", Limit: 10})
//	stream, _ := c.Play(ctx, "SSIS-001")            // 拿到最优可播放流
//	res, _ := c.Download(ctx, "SSIS-001", "./out", av.DefaultDownloadOptions())
//
// Cloudflare 绕过（分层策略，参考 EchterAlsFake/unofficial-api-for-missav、Alos21750/UAV-Downloader）：
//
//  1. 搜索优先走 Recombee 后端 API（client-rapi-missav.recombee.com，HMAC-SHA1 签名），
//     该域名不在 missav 的 Cloudflare 之后，直连即可、无需代理与指纹伪装（见 recombee.go）。
//  2. 详情页/播放列表等 HTML 仍受 CF 保护，通过 Options.Requester 注入浏览器 TLS/HTTP2 指纹
//     （子包 browser，基于 bogdanfinn/tls-client）。实测需较新指纹（chrome_150 / firefox_135）
//     才能从数据中心 IP 放行；旧指纹会命中 "Just a moment" 托管质询。
//  3. surrit.com 的 HLS 分片校验接近浏览器会话，同样依赖上述指纹 + 正确 Referer 才能下载。
//  4. 若 IP 信誉过差被硬封，可设置 VL_PROXY 走代理，或用可过质询的住宅网络。
//  5. 手动/自动化注入 cf_clearance（彻底免除反复质询）：在 Options.CFCookies 按 host 填入
//     cf_clearance 及其绑定的 UA，或用 Options.CookieProvider 动态提供（见 cfstore.go 的 CFStore，
//     可 JSON 持久化、作为 webview 抓取回填的落点）。命中主机的请求会自动携带该 Cookie 并锁定其 UA
//     ——cf_clearance 与签发时的 UA、出口 IP 绑定，换网络/换代理后需重新抓取。
package av
