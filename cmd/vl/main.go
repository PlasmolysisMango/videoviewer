package main

// vl - videoloader pkg 示例命令行工具，演示搜索 / 播放解析 / 下载能力。
//
// 子命令:
//
//	vl search    -q "SSIS" [-n 20] [-s missav] [--proxy URL]
//	vl latest    [-n 20] [-s missav] [--proxy URL]
//	vl detail    -c SSIS-001 [-s missav] [--proxy URL]
//	vl play      -c SSIS-001 [-s missav] [--max-height H] [--proxy URL]
//	vl download  -c SSIS-001 [-o ./out] [-s missav] [--max-height H] [--remux] [-j 8] [--proxy URL]
//
// 说明：missav / jable 站点位于 Cloudflare 之后，纯标准库客户端可能被 403 拦截；
// 生产使用请通过代理 (--proxy) 或注入自定义 Transport（见 pkg/av.Options.Transport）。

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"videoviewer/pkg/av"
	"videoviewer/pkg/av/browser"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "search":
		err = cmdSearch(os.Args[2:])
	case "latest":
		err = cmdLatest(os.Args[2:])
	case "detail":
		err = cmdDetail(os.Args[2:])
	case "play":
		err = cmdPlay(os.Args[2:])
	case "download":
		err = cmdDownload(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		err = fmt.Errorf("未知子命令: %s", os.Args[1])
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

// newClient 构造 Client。profile 非 "std"/"none" 时默认启用 tls-client 浏览器指纹以绕过 Cloudflare。
func newClient(proxy, profile string) *av.Client {
	httpOpt := av.Options{Proxy: proxy, Timeout: 25 * time.Second}
	if profile != "std" && profile != "none" {
		r, err := browser.New(browser.Options{Profile: profile, Proxy: proxy, Timeout: 25 * time.Second, FollowRedirect: true})
		if err != nil {
			panic(err)
		}
		httpOpt.Requester = r
		httpOpt.Proxy = "" // 代理已下沉到 requester
	}
	c, err := av.NewClient(av.ClientOptions{HTTP: httpOpt})
	if err != nil {
		panic(err)
	}
	return c
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	q := fs.String("q", "", "关键词")
	n := fs.Int("n", 20, "数量上限")
	src := fs.String("s", "", "指定数据源")
	proxy := fs.String("proxy", "", "HTTP/SOCKS5 代理")
	profile := fs.String("profile", "chrome_150", "浏览器指纹(std=不用tls-client; 见 browser.AvailableProfiles)")
	_ = fs.Parse(args)
	if *q == "" {
		return fmt.Errorf("缺少 -q 关键词")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	videos, err := newClient(*proxy, *profile).Search(ctx, av.Query{Keyword: *q, Limit: *n, Source: *src})
	if err != nil {
		return err
	}
	printJSON(videos)
	fmt.Fprintf(os.Stderr, "共 %d 条\n", len(videos))
	return nil
}

func cmdLatest(args []string) error {
	fs := flag.NewFlagSet("latest", flag.ExitOnError)
	n := fs.Int("n", 20, "数量上限")
	src := fs.String("s", "", "指定数据源")
	proxy := fs.String("proxy", "", "HTTP/SOCKS5 代理")
	profile := fs.String("profile", "chrome_150", "浏览器指纹(std=不用tls-client; 见 browser.AvailableProfiles)")
	_ = fs.Parse(args)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	videos, err := newClient(*proxy, *profile).Latest(ctx, av.Query{Limit: *n, Source: *src})
	if err != nil {
		return err
	}
	printJSON(videos)
	fmt.Fprintf(os.Stderr, "共 %d 条\n", len(videos))
	return nil
}

func cmdDetail(args []string) error {
	fs := flag.NewFlagSet("detail", flag.ExitOnError)
	code := fs.String("c", "", "番号")
	src := fs.String("s", "", "指定数据源")
	proxy := fs.String("proxy", "", "HTTP/SOCKS5 代理")
	profile := fs.String("profile", "chrome_150", "浏览器指纹(std=不用tls-client; 见 browser.AvailableProfiles)")
	_ = fs.Parse(args)
	if *code == "" {
		return fmt.Errorf("缺少 -c 番号")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	v, err := newClient(*proxy, *profile).Detail(ctx, *code, *src)
	if err != nil {
		return err
	}
	printJSON(v)
	return nil
}

func cmdPlay(args []string) error {
	fs := flag.NewFlagSet("play", flag.ExitOnError)
	code := fs.String("c", "", "番号")
	src := fs.String("s", "", "指定数据源")
	maxH := fs.Int("max-height", 0, "最高清晰度高度上限")
	proxy := fs.String("proxy", "", "HTTP/SOCKS5 代理")
	profile := fs.String("profile", "chrome_150", "浏览器指纹(std=不用tls-client; 见 browser.AvailableProfiles)")
	_ = fs.Parse(args)
	if *code == "" {
		return fmt.Errorf("缺少 -c 番号")
	}
	c := newClient(*proxy, *profile)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	streams, err := c.Resolve(ctx, *code, *src)
	if err != nil {
		return err
	}
	printJSON(streams)
	best, err := c.Play(ctx, *code, av.DownloadOptions{Source: *src, MaxQualityHeight: *maxH})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "可选 %d 路；推荐播放: %s\n", len(streams), best)
	return nil
}

func cmdDownload(args []string) error {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	code := fs.String("c", "", "番号")
	out := fs.String("o", "./out", "输出目录")
	src := fs.String("s", "", "指定数据源")
	maxH := fs.Int("max-height", 0, "最高清晰度高度上限")
	conc := fs.Int("j", 8, "分片并发")
	remux := fs.Bool("remux", false, "下载后调用 ffmpeg 转 mp4")
	proxy := fs.String("proxy", "", "HTTP/SOCKS5 代理")
	profile := fs.String("profile", "chrome_150", "浏览器指纹(std=不用tls-client; 见 browser.AvailableProfiles)")
	_ = fs.Parse(args)
	if *code == "" {
		return fmt.Errorf("缺少 -c 番号")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opt := av.DownloadOptions{
		Source:           *src,
		MaxQualityHeight: *maxH,
		Concurrency:      *conc,
		RemuxToMP4:       *remux,
		Progress:         func(done, total int) { fmt.Fprintf(os.Stderr, "\r下载 %d/%d", done, total) },
	}
	res, err := newClient(*proxy, *profile).Download(ctx, *code, *out, opt)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		if res != nil {
			fmt.Fprintf(os.Stderr, "部分完成: %s\n", res.FilePath)
		}
		return err
	}
	printJSON(res)
	return nil
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func usage() {
	fmt.Fprint(os.Stderr, `vl - videoloader 示例 CLI

子命令:
  search    -q 关键词 [-n 数量] [-s 源] [--proxy URL]
  latest    [-n 数量] [-s 源] [--proxy URL]
  detail    -c 番号 [-s 源] [--proxy URL]
  play      -c 番号 [-s 源] [--max-height H] [--proxy URL]
  download  -c 番号 [-o 目录] [-s 源] [--max-height H] [--remux] [-j 并发] [--proxy URL]

已注册数据源: missav, jable, hohoj

通用参数: --profile 浏览器指纹(默认 chrome_110, 设为 std 则不绕CF), --proxy HTTP/SOCKS5代理
`)
}
