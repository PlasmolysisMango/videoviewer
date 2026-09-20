package subs

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// ToUTF8 normalizes an SRT payload to UTF-8 without language context.
// See ToUTF8WithLang for the language-aware variant.
func ToUTF8(b []byte) []byte {
	return ToUTF8WithLang(b, "")
}

// ToUTF8WithLang normalizes an SRT payload to UTF-8:
//  1. UTF-8/ASCII passes through (BOM stripped);
//  2. UTF-16 (BOM-required) is decoded via x/text;
//  3. otherwise legacy CJK encodings are probed. When lang hints the content
//     language ("zh", "ja", "ko"…) its native encoding is tried first; in the
//     contextless case Shift-JIS / GB18030 / Big5 / EUC-KR are tried in turn
//     and results polluted by halfwidth-kana mojibake are rejected.
//
// The payload content itself is never altered apart from decoding.
func ToUTF8WithLang(b []byte, lang string) []byte {
	if len(b) == 0 {
		return b
	}
	// BOM: UTF-8 / UTF-16 LE / UTF-16 BE.
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		return b[3:]
	}
	if bytes.HasPrefix(b, []byte{0xFF, 0xFE}) {
		if out, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Bytes(b[2:]); err == nil {
			return out
		}
		return b[2:]
	}
	if bytes.HasPrefix(b, []byte{0xFE, 0xFF}) {
		if out, err := unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewDecoder().Bytes(b[2:]); err == nil {
			return out
		}
		return b[2:]
	}
	if utf8.Valid(b) {
		return b
	}

	// Probe order driven by the language hint; every candidate must decode
	// cleanly and pass the mojibake screen.
	var order []func() decoder
	switch {
	case strings.HasPrefix(lang, "zh"):
		order = []func() decoder{
			func() decoder { return &xtextDecoder{simplifiedchinese.GB18030.NewDecoder()} },
			func() decoder { return &xtextDecoder{traditionalchinese.Big5.NewDecoder()} },
			func() decoder { return &xtextDecoder{japanese.ShiftJIS.NewDecoder()} },
			func() decoder { return &xtextDecoder{korean.EUCKR.NewDecoder()} },
		}
	case strings.HasPrefix(lang, "ja"), strings.HasPrefix(lang, "jp"):
		order = []func() decoder{
			func() decoder { return &xtextDecoder{japanese.ShiftJIS.NewDecoder()} },
			func() decoder { return &xtextDecoder{simplifiedchinese.GB18030.NewDecoder()} },
			func() decoder { return &xtextDecoder{traditionalchinese.Big5.NewDecoder()} },
			func() decoder { return &xtextDecoder{korean.EUCKR.NewDecoder()} },
		}
	case strings.HasPrefix(lang, "ko"):
		order = []func() decoder{
			func() decoder { return &xtextDecoder{korean.EUCKR.NewDecoder()} },
			func() decoder { return &xtextDecoder{simplifiedchinese.GB18030.NewDecoder()} },
			func() decoder { return &xtextDecoder{japanese.ShiftJIS.NewDecoder()} },
		}
	default:
		order = []func() decoder{
			func() decoder { return &xtextDecoder{japanese.ShiftJIS.NewDecoder()} },
			func() decoder { return &xtextDecoder{simplifiedchinese.GB18030.NewDecoder()} },
			func() decoder { return &xtextDecoder{traditionalchinese.Big5.NewDecoder()} },
			func() decoder { return &xtextDecoder{korean.EUCKR.NewDecoder()} },
		}
	}

	best := b
	bestScore := -1.0
	for _, newDec := range order {
		out, err := newDec().decode(b)
		if err != nil || len(out) == 0 || !utf8.Valid(out) {
			continue
		}
		score := textScore(out)
		if score < 0 {
			continue // mojibake screen
		}
		if score > bestScore {
			best, bestScore = out, score
		}
	}
	return best
}

type decoder interface {
	decode(b []byte) ([]byte, error)
}

type xtextDecoder struct {
	d interface {
		Bytes(b []byte) ([]byte, error)
	}
}

func (x *xtextDecoder) decode(b []byte) ([]byte, error) { return x.d.Bytes(b) }

// textScore scores decoded text (higher = more plausible). It returns -1 for
// mojibake: a result dominated by halfwidth katakana (U+FF61–U+FF9F) is the
// signature of GBK/Big5 bytes misread through Shift-JIS.
func textScore(b []byte) float64 {
	runes := bytes.Runes(b)
	if len(runes) == 0 {
		return -1
	}
	good, halfKana := 0, 0
	for _, r := range runes {
		switch {
		case r >= 0x20 && r < 0x7F: // ASCII printable
			good++
		case r >= 0x4E00 && r <= 0x9FFF: // CJK unified ideographs
			good++
		case r >= 0x3040 && r <= 0x30FF: // Hiragana / fullwidth katakana
			good++
		case r >= 0xAC00 && r <= 0xD7AF: // Hangul
			good++
		case r >= 0xFF01 && r <= 0xFF60: // Fullwidth forms
			good++
		case r >= 0x3000 && r <= 0x303F: // CJK punctuation
			good++
		case r == '\n' || r == '\r' || r == '\t':
			good++
		case r >= 0xFF61 && r <= 0xFF9F: // halfwidth katakana
			halfKana++
		}
	}
	ratio := float64(good) / float64(len(runes))
	if float64(halfKana)/float64(len(runes)) > 0.25 {
		return -1
	}
	return ratio
}
