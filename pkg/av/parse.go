package av

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	// codeRe 匹配番号，如 SSIS-001、ABC-123、STCVS-007。参考 missav-bot 的 CODE_PATTERN 并扩展。
	codeRe = regexp.MustCompile(`(?i)([A-Z]+(?:-[A-Z]+)*-?\d+[A-Za-z]?)`)
	// ogTitleRe 提取 og:title 的 content。
	ogTitleRe = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:title["'][^>]+content=["']([^"']+)["']`)
	// ogImageRe 提取 og:image。
	ogImageRe = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:image["'][^>]+content=["']([^"']+)["']`)
	// durationMinRe 提取 "123分" / "123 分钟" / "2:13:00" 之类的时长。
	durationMinRe = regexp.MustCompile(`(\d+)\s*分`)
	durationHMRe  = regexp.MustCompile(`(?i)(\d{1,2}):(\d{2}):(\d{2})`)
	durationHRe   = regexp.MustCompile(`(\d+)\s*(?:小时|h\b)`)
)

// ExtractCode 从任意文本（标题或 URL）中提取规范化番号；未命中返回 ""。
func ExtractCode(text string) string {
	m := codeRe.FindStringSubmatch(strings.ToUpper(text))
	if m == nil {
		return ""
	}
	return m[1]
}

// stripCodeFromTitle 从标题中移除番号前缀，返回干净标题。
func stripCodeFromTitle(title, code string) string {
	if code == "" {
		return strings.TrimSpace(title)
	}
	return strings.TrimSpace(strings.Replace(title, code, "", 1))
}

// parseDurationMinutes 从文本中解析时长。支持 "120分钟"、"2小时"、"2:00:00"。
func parseDurationMinutes(text string) time.Duration {
	if m := durationHMRe.FindStringSubmatch(text); m != nil {
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		ss, _ := strconv.Atoi(m[3])
		return time.Duration(h)*time.Hour + time.Duration(mm)*time.Minute + time.Duration(ss)*time.Second
	}
	total := 0
	if m := durationHRe.FindStringSubmatch(text); m != nil {
		h, _ := strconv.Atoi(m[1])
		total += h * 60
	}
	if m := durationMinRe.FindStringSubmatch(text); m != nil {
		mm, _ := strconv.Atoi(m[1])
		total += mm
	}
	return time.Duration(total) * time.Minute
}

// ogTitle 返回页面 og:title 内容（去 HTML 实体的近似）。
func ogTitle(html string) string {
	if m := ogTitleRe.FindStringSubmatch(html); m != nil {
		return cleanText(m[1])
	}
	return ""
}

// ogImage 返回页面 og:image 地址。
func ogImage(html string) string {
	if m := ogImageRe.FindStringSubmatch(html); m != nil {
		return cleanText(m[1])
	}
	return ""
}

func cleanText(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#039;", "'", "&lt;", "<", "&gt;", ">", "&nbsp;", " ")
	return strings.TrimSpace(r.Replace(s))
}

// applyLimit 对结果切片应用 Query.Limit。
func applyLimit(videos []Video, limit int) []Video {
	if limit > 0 && len(videos) > limit {
		return videos[:limit]
	}
	return videos
}

// parseVideoCards 使用 goquery 从列表页解析视频卡片。
// linkSel / cardSel 由各数据源根据自身 DOM 结构调整。
// 这里实现一个通用策略：查找所有含番号链接的 <a>，就近提取标题与封面。
func parseVideoCards(doc *goquery.Document, baseURL, source string) []Video {
	seen := map[string]bool{}
	var videos []Video

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		if href == "" || isNavHref(href) {
			return
		}
		code := ExtractCode(href)
		if code == "" || seen[code] {
			return
		}
		v := Video{
			Code:      code,
			DetailURL: absURL(baseURL, href),
			Source:    source,
		}
		// 标题：优先子树内的 title/alt/text，其次链接文本
		container := s.Closest("div.group").First()
		if container.Length() == 0 {
			container = s
		}
		if t := firstAttr(container, "img", "title"); t != "" {
			v.Title = cleanText(t)
		} else if t := firstAttr(container, "img", "alt"); t != "" {
			v.Title = cleanText(t)
		} else if txt := strings.TrimSpace(s.Text()); txt != "" {
			v.Title = cleanText(txt)
		}
		v.Title = stripCodeFromTitle(v.Title, code)

		// 封面
		if img := container.Find("img").First(); img.Length() > 0 {
			v.CoverURL = pickImgSrc(img)
		}
		// 时长
		if dur := container.Find("span").FilterFunction(func(_ int, e *goquery.Selection) bool {
			return durationMinRe.MatchString(e.Text()) || durationHMRe.MatchString(e.Text())
		}).First(); dur.Length() > 0 {
			v.Duration = parseDurationMinutes(dur.Text())
		}

		seen[code] = true
		videos = append(videos, v)
	})

	return videos
}

// dmPrefixRe 匹配 missav 的路由前缀段（如 /dm278/），它们不是番号。
var dmPrefixRe = regexp.MustCompile(`(?i)^dm\d+$`)

var digitRe = regexp.MustCompile(`\d`)

// isNavHref 判断一个链接是否为导航/分类项而非视频卡片：
// 跳过 /search/ 链接，以及末段不含数字或为 dm\d+ 路由前缀的链接（如 /dm278/chinese-subtitle）。
func isNavHref(href string) bool {
	if strings.Contains(href, "/search/") {
		return true
	}
	seg := lastPathSegment(href)
	if seg == "" || dmPrefixRe.MatchString(seg) {
		return true
	}
	return !digitRe.MatchString(seg)
}

// lastPathSegment 返回 URL 去掉查询串后的最后一个非空路径段。
func lastPathSegment(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.TrimRight(u, "/")
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// firstAttr 返回 sel 下匹配 matcher 的第一个元素的 attr 值。
func firstAttr(sel *goquery.Selection, matcher, attr string) string {
	v := ""
	sel.Find(matcher).First().Each(func(_ int, e *goquery.Selection) {
		if a, ok := e.Attr(attr); ok && a != "" {
			v = a
		}
	})
	return v
}

// pickImgSrc 从 img 元素挑选真实图片地址，跳过 base64 占位与 data-src 之外的懒加载字段。
func pickImgSrc(img *goquery.Selection) string {
	for _, attr := range []string{"data-src", "data-original", "data-lazy", "src"} {
		if v, ok := img.Attr(attr); ok && v != "" && !strings.HasPrefix(v, "data:") {
			return v
		}
	}
	return ""
}

// absURL 将 possibly-relative 的 href 依据 baseURL 归一为绝对地址。
func absURL(baseURL, ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	base := strings.TrimRight(baseURL, "/")
	if strings.HasPrefix(ref, "/") {
		return schemeHost(base) + ref
	}
	return base + "/" + ref
}

// schemeHost 返回 URL 的 scheme://host 部分。
func schemeHost(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return u[:i+3] + rest[:j]
		}
		return u
	}
	return strings.TrimRight(u, "/")
}
