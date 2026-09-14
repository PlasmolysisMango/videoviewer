package av

// missav Recombee 后端检索：绕过站点 Cloudflare 的关键手法。
//
// 参考 https://github.com/EchterAlsFake/unofficial-api-for-missav 。
// missav 前端的搜索/推荐实际由其 Recombee 实例（client-rapi-missav.recombee.com）提供，
// 该域名不在 missav 的 Cloudflare 质询之后，因此直接签名调用即可拿到结构化结果，
// 无需伪装浏览器指纹、也不会命中 "Just a moment" 托管质询。
//
// 请求路径需用 public token 做 HMAC-SHA1 签名（复现前端 JS 的 _signUrl）。

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	recombeeDB    = "missav-default"
	recombeeToken = "Ikkg568nlM51RHvldlPvc2GzZPE9R4XGzaH9Qj4zK9npbbbTly1gj9K4mgRn0QlV"
)

// recombeeHost / recombeeScheme 声明为变量以便离线测试注入 httptest 地址。
var (
	recombeeHost   = "client-rapi-missav.recombee.com"
	recombeeScheme = "https"
)

// recombeeSearchResp 是 Recombee 搜索返回结构（仅取所需字段）。
type recombeeSearchResp struct {
	RecommID string `json:"recommId"`
	Recomms  []struct {
		ID     string `json:"id"`
		Values struct {
			Title            string   `json:"title"`
			TitleEn          string   `json:"title_en"`
			TitleZh          string   `json:"title_zh"`
			Duration         float64  `json:"duration"`
			ReleasedAt       float64  `json:"released_at"`
			Actresses        []string `json:"actresses"`
			Genres           []string `json:"genres"`
			Labels           []string `json:"labels"`
			Markers          []string `json:"markers"`
			Tags             []string `json:"tags"`
			HasChineseSub    bool     `json:"has_chinese_subtitle"`
			IsUncensoredLeak bool     `json:"is_uncensored_leak"`
		} `json:"values"`
	} `json:"recomms"`
}

// recombeeSignature 用 public token 对 unsigned 串做 HMAC-SHA1，返回十六进制摘要。
func recombeeSignature(unsigned string) string {
	mac := hmac.New(sha1.New, []byte(recombeeToken))
	mac.Write([]byte(unsigned))
	return hex.EncodeToString(mac.Sum(nil))
}

// recombeeSignedPath 复现前端 _signUrl：拼时间戳 + 追加 frontend_sign。
func recombeeSignedPath(path string, ts int64) string {
	unsigned := fmt.Sprintf("/%s%s?frontend_timestamp=%d", recombeeDB, path, ts)
	return unsigned + "&frontend_sign=" + recombeeSignature(unsigned)
}

// anonUserID 生成匿名推荐用户 id（形如 anon_<16位hex>）。
func anonUserID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "anon_" + hex.EncodeToString(b)
}

// searchMissAVRecombee 通过 Recombee API 检索 missav，返回结构化视频列表。
// baseURL 仅用于推导详情页域名（保持与数据源配置的镜像一致）。
func searchMissAVRecombee(ctx context.Context, hc *HTTPClient, baseURL, keyword string, count int) ([]Video, error) {
	if count <= 0 || count > 100 {
		count = 36
	}
	path := fmt.Sprintf("/search/users/%s/items/", url.PathEscape(anonUserID()))
	apiURL := fmt.Sprintf("%s://%s%s", recombeeScheme, recombeeHost, recombeeSignedPath(path, time.Now().Unix()))
	payload := fmt.Sprintf(`{"searchQuery":%q,"count":%d,"cascadeCreate":true,"returnProperties":true}`, keyword, count)
	headers := map[string]string{
		"Accept":  "application/json",
		"Origin":  "https://missav.ws",
		"Referer": "https://missav.ws/",
	}

	body, err := hc.PostJSON(ctx, apiURL, headers, []byte(payload))
	if err != nil {
		return nil, err
	}
	var resp recombeeSearchResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("missav recombee: bad json: %w", err)
	}

	origin := schemeHost(baseURL)
	out := make([]Video, 0, len(resp.Recomms))
	for _, r := range resp.Recomms {
		if strings.TrimSpace(r.ID) == "" {
			continue
		}
		code := ExtractCode(strings.ReplaceAll(r.ID, "_", "-"))
		if code == "" {
			code = ExtractCode(r.Values.Title)
		}
		if code == "" {
			continue
		}
		title := firstNonEmpty(r.Values.Title, r.Values.TitleEn, r.Values.TitleZh)
		v := Video{
			Code:      code,
			Title:     stripCodeFromTitle(title, code),
			DetailURL: origin + "/cn/" + r.ID,
			Source:    "missav",
			Actresses: dedup(r.Values.Actresses),
			Tags:      dedup(append(append([]string{}, r.Values.Genres...), append(r.Values.Tags, r.Values.Markers...)...)),
		}
		if r.Values.Duration > 0 {
			v.Duration = time.Duration(r.Values.Duration) * time.Second
		}
		if r.Values.ReleasedAt > 0 {
			t := time.Unix(int64(r.Values.ReleasedAt), 0)
			v.ReleaseDate = &t
		}
		out = append(out, v)
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// dedup 去重并剔除空串，保持原有顺序。
func dedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
