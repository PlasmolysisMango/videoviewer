// Package browser 提供基于 bogdanfinn/tls-client 的浏览器 TLS/HTTP2 指纹 HTTP 客户端，
// 用于绕过 Cloudflare 对标准 net/http 指纹的拦截（对齐 NASSAV 中 curl_cffi 的 impersonate=chrome110）。
//
// tls-client 底层使用其 fork 的 github.com/bogdanfinn/fhttp（类型与标准 net/http 不同），
// 本包通过一个适配器把它包装成标准库类型的 av.Requester，从而可无侵入注入：
//
//	r, _ := browser.New(browser.Options{Profile: "chrome_110", Proxy: "http://127.0.0.1:7890"})
//	c, _ := av.NewClient(av.ClientOptions{HTTP: av.Options{Requester: r}})
package browser

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"videoviewer/pkg/av"
)

// Options 配置浏览器指纹客户端。
type Options struct {
	// Profile 指纹标识，如 "chrome_110" / "firefox_120" / "safari_170"。
	// 空则使用 "chrome_110"（对齐 NASSAV）。可用值见 profiles.MappedTLSClients。
	Profile string
	// Proxy 代理地址，支持 http:// https:// socks5:// socks5h://。空表示直连。
	Proxy string
	// Timeout 单次请求超时，默认 20s。
	Timeout time.Duration
	// InsecureSkipVerify 跳过 TLS 校验（谨慎使用）。
	InsecureSkipVerify bool
	// FollowRedirect 是否自动跟随重定向。置 false（零值）则不跟随并原样返回 3xx；
	// 抓取页面时建议显式设为 true（站点首页常 301 跳转到语言路径）。
	FollowRedirect bool
}

// New 返回一个实现 av.Requester 的浏览器指纹 HTTP 执行器。
func New(opt Options) (av.Requester, error) {
	profile, err := lookupProfile(opt.Profile)
	if err != nil {
		return nil, err
	}

	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	opts := []tls_client.HttpClientOption{
		tls_client.WithClientProfile(profile),
		tls_client.WithTimeoutSeconds(int(timeout.Seconds()) + 1),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}
	if opt.InsecureSkipVerify {
		opts = append(opts, tls_client.WithInsecureSkipVerify())
	}
	if opt.Proxy != "" {
		opts = append(opts, tls_client.WithProxyUrl(opt.Proxy))
	}
	if !opt.FollowRedirect {
		opts = append(opts, tls_client.WithNotFollowRedirects())
	}

	tlsHTTP, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		return nil, fmt.Errorf("browser: init tls-client: %w", err)
	}
	return &adapter{client: tlsHTTP}, nil
}

// adapter 把 tls-client(fhttp) 包装成标准 net/http 的 Requester。
type adapter struct {
	client tls_client.HttpClient
}

// 编译期确保满足接口。
var _ av.Requester = (*adapter)(nil)

// Do 执行一次请求：标准请求 → fhttp 请求 → tls-client → 标准响应。
func (a *adapter) Do(req *http.Request) (*http.Response, error) {
	freq, err := toFrequent(req)
	if err != nil {
		return nil, err
	}
	fresp, err := a.client.Do(freq)
	if err != nil {
		return nil, err
	}
	return fromFResponse(fresp, req)
}

// toFrequent 把标准 *http.Request 转换为 *fhttp.Request。
func toFrequent(req *http.Request) (*fhttp.Request, error) {
	var body io.Reader
	if req.Body != nil && req.Body != http.NoBody {
		body = req.Body
	}
	freq, err := fhttp.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), body)
	if err != nil {
		return nil, err
	}
	if req.Header != nil {
		// http.Header 与 fhttp.Header 底层同为 map[string][]string，可直接转换。
		freq.Header = fhttp.Header(req.Header.Clone())
	}
	if req.Host != "" {
		freq.Host = req.Host
	}
	return freq, nil
}

// fromFResponse 把 *fhttp.Response 转换回标准 *http.Response。
func fromFResponse(fresp *fhttp.Response, req *http.Request) (*http.Response, error) {
	if fresp == nil {
		return nil, fmt.Errorf("browser: nil response")
	}
	resp := &http.Response{
		Status:        fresp.Status,
		StatusCode:    fresp.StatusCode,
		Proto:         fresp.Proto,
		ProtoMajor:    fresp.ProtoMajor,
		ProtoMinor:    fresp.ProtoMinor,
		Header:        http.Header(fresp.Header),
		Body:          fresp.Body,
		ContentLength: fresp.ContentLength,
		Close:         fresp.Close,
		Request:       req,
	}
	return resp, nil
}

// lookupProfile 按名称解析指纹 profile，兼容 "chrome110" 与 "chrome_110" 两种写法。
func lookupProfile(name string) (profiles.ClientProfile, error) {
	if name == "" {
		name = "chrome_110"
	}
	key := normalizeProfile(name)
	if p, ok := profiles.MappedTLSClients[key]; ok {
		return p, nil
	}
	// 尝试去掉下划线再匹配（如 "chrome110"）。
	if p, ok := profiles.MappedTLSClients[strings.ReplaceAll(key, "_", "")]; ok {
		return p, nil
	}
	return profiles.ClientProfile{}, fmt.Errorf("browser: unknown profile %q, see profiles.MappedTLSClients", name)
}

func normalizeProfile(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// AvailableProfiles 返回可用的指纹标识列表（便于上层展示/校验）。
func AvailableProfiles() []string {
	out := make([]string, 0, len(profiles.MappedTLSClients))
	for k := range profiles.MappedTLSClients {
		out = append(out, k)
	}
	return out
}
