package javdb

import (
	"strings"
	"sync"

	"github.com/longbridgeapp/opencc"
)

// Chinese conversion runs through OpenCC (github.com/longbridgeapp/opencc,
// pure Go, dictionary data embedded in the package). Its ST/TSCharacters +
// phrase dictionaries resolve one-to-many pairs at the word level —
// "乾燥"→"干燥" while "乾隆" stays intact, "頭髮"→"头发" vs "出發"→"出发" —
// which a flat character map cannot do (the previous generated 3881-char
// table leaked variants like 為/製/麽 and mis-converted ambiguous chars).
//
// JavDB serves traditional Chinese genre/tag names while users browse in
// simplified; actor search falls back to a simplified->traditional rewrite;
// subtitle payloads (zh-TW sources) are simplified before caching.

// extVariants 把常见的 CJK 扩展区（B 区）异体字归一到标准字形。
// Android/Flutter 系统字体不含扩展区字形，字幕里的 𫔭（開的异体，
// 台繁字幕源高频）会渲染成豆腐块，用户视为乱码；OpenCC 不覆盖扩展区
// 异体，先归一化再走简繁转换。目标直接给简体字形，未收录的字符原样
// 保留，后续遇到新的豆腐块字在此追加。
var extVariants = map[rune]rune{
	0x2B52D: '开', // 𫔭 開的异体
	0x20BB7: '吉', // 𠮷 吉的异体
}

var (
	openccOnce sync.Once
	openccT2S  *opencc.OpenCC // traditional -> simplified
	openccS2T  *opencc.OpenCC // simplified -> traditional (actor search fallback)
)

func openccConverters() (t2s, s2t *opencc.OpenCC) {
	openccOnce.Do(func() {
		openccT2S, _ = opencc.New("t2s")
		openccS2T, _ = opencc.New("s2t")
	})
	return openccT2S, openccS2T
}

// normalizeExtVariants rewrites known extension-B variants; the fast path
// skips strings without any non-BMP runes.
func normalizeExtVariants(s string) string {
	hasExt := false
	for _, r := range s {
		if r > 0xFFFF {
			hasExt = true
			break
		}
	}
	if !hasExt {
		return s
	}
	return strings.Map(func(r rune) rune {
		if t, ok := extVariants[r]; ok {
			return t
		}
		return r
	}, s)
}

// ToSimplified rewrites traditional Chinese text to simplified ones, leaving
// any other rune (kana, latin, digits) untouched. On converter failure the
// input is returned unchanged.
func ToSimplified(s string) string {
	s = normalizeExtVariants(s)
	conv, _ := openccConverters()
	if conv == nil {
		return s
	}
	out, err := conv.Convert(s)
	if err != nil {
		return s
	}
	return out
}

// toTraditional rewrites simplified Chinese text to traditional ones (used
// by actor approximate search before giving up), leaving kana/latin
// untouched.
func toTraditional(s string) string {
	_, conv := openccConverters()
	if conv == nil {
		return s
	}
	out, err := conv.Convert(s)
	if err != nil {
		return s
	}
	return out
}
