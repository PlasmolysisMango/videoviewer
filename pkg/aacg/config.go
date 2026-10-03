package aacg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

var errNoConfig = errors.New("aacg: no inline window.appConfig assignment")

type jsToken struct {
	text   string
	quoted bool
}

func scriptTokens(script string) ([]jsToken, error) {
	if !utf8.ValidString(script) {
		return nil, errors.New("aacg: script is not valid UTF-8")
	}
	var tokens []jsToken
	for i := 0; i < len(script); {
		r, size := utf8.DecodeRuneInString(script[i:])
		if unicode.IsSpace(r) || r == '\ufeff' {
			i += size
			continue
		}
		if strings.HasPrefix(script[i:], "//") || strings.HasPrefix(script[i:], "<!--") {
			for i < len(script) && !strings.ContainsRune("\r\n\u2028\u2029", runeAt(script[i:])) {
				_, size = utf8.DecodeRuneInString(script[i:])
				i += size
			}
			continue
		}
		if strings.HasPrefix(script[i:], "/*") {
			end := strings.Index(script[i+2:], "*/")
			if end < 0 {
				return nil, errors.New("aacg: unterminated script comment")
			}
			i += end + 4
			continue
		}
		if script[i] == '\'' || script[i] == '"' {
			value, end, err := readJSString(script, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, jsToken{text: value, quoted: true})
			i = end
			continue
		}
		// Reject lexical forms that could hide executable assignments or string decoys.
		if script[i] == '`' || script[i] == '/' || script[i] == '\\' {
			return nil, fmt.Errorf("aacg: unsupported script syntax %q", script[i])
		}
		end := i + size
		if jsWord(r) {
			for end < len(script) {
				r, size = utf8.DecodeRuneInString(script[end:])
				if !jsWord(r) {
					break
				}
				end += size
			}
		}
		tokens = append(tokens, jsToken{text: script[i:end]})
		i = end
	}
	return tokens, nil
}

func runeAt(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func jsWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' || r == '$' || r == '\u200c' || r == '\u200d'
}

func jsIdentifier(token jsToken) bool {
	if token.quoted {
		return true
	}
	first := runeAt(token.text)
	return first == '_' || first == '$' || unicode.IsLetter(first)
}

func readJSString(script string, start int) (string, int, error) {
	var value strings.Builder
	quote := script[start]
	for i := start + 1; i < len(script); {
		b := script[i]
		i++
		switch b {
		case quote:
			return value.String(), i, nil
		case '\n', '\r':
			return "", 0, errors.New("aacg: unescaped newline in script string")
		case '\\':
			if i == len(script) {
				break
			}
			escape := script[i]
			i++
			switch escape {
			case '\n':
				continue
			case '\r':
				if i < len(script) && script[i] == '\n' {
					i++
				}
				continue
			case 'b', 'f', 'n', 'r', 't', 'v':
				value.WriteByte(map[byte]byte{'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v'}[escape])
			case '0':
				if i < len(script) && script[i] >= '0' && script[i] <= '9' {
					return "", 0, errors.New("aacg: unsupported legacy numeric escape")
				}
				value.WriteByte(0)
			case '1', '2', '3', '4', '5', '6', '7', '8', '9':
				return "", 0, errors.New("aacg: unsupported legacy numeric escape")
			case 'x', 'u':
				n := 2
				if escape == 'u' {
					n = 4
				}
				if i+n > len(script) {
					return "", 0, errors.New("aacg: incomplete hexadecimal string escape")
				}
				code, err := strconv.ParseUint(script[i:i+n], 16, 16)
				if err != nil {
					return "", 0, errors.New("aacg: invalid hexadecimal string escape")
				}
				i += n
				r := rune(code)
				if r >= 0xd800 && r <= 0xdbff && i+6 <= len(script) && script[i:i+2] == `\u` {
					low, err := strconv.ParseUint(script[i+2:i+6], 16, 16)
					if err == nil && low >= 0xdc00 && low <= 0xdfff {
						r = utf16.DecodeRune(r, rune(low))
						i += 6
					}
				}
				if utf16.IsSurrogate(r) {
					return "", 0, errors.New("aacg: unpaired surrogate in script string")
				}
				value.WriteRune(r)
			default:
				r, size := utf8.DecodeRuneInString(script[i-1:])
				i += size - 1
				if r != '\u2028' && r != '\u2029' {
					value.WriteRune(r)
				}
			}
		default:
			value.WriteByte(b)
		}
	}
	return "", 0, errors.New("aacg: unterminated script string")
}

func tokenIs(tokens []jsToken, i int, text string) bool {
	return i >= 0 && i < len(tokens) && !tokens[i].quoted && tokens[i].text == text
}

func configMutation(tokens []jsToken, i int) bool {
	operator := ""
	for end := i; end < len(tokens) && end < i+4 && !tokens[end].quoted; end++ {
		operator += tokens[end].text
		switch operator {
		case "++", "--", "+=", "-=", "*=", "**=", "%=", "&=", "&&=", "|=", "||=", "^=", "??=", "<<=", ">>=", ">>>=":
			return true
		}
	}
	return false
}

func extractTargets(doc *goquery.Document) ([]Target, error) {
	if doc == nil {
		return nil, errNoConfig
	}
	var data, key string
	assignments := 0
	var scanErr error
	doc.Find("script:not([src])").EachWithBreak(func(_ int, script *goquery.Selection) bool {
		kind := strings.ToLower(strings.TrimSpace(script.AttrOr("type", "")))
		if kind != "" && kind != "module" && kind != "text/javascript" && kind != "application/javascript" && kind != "text/ecmascript" && kind != "application/ecmascript" {
			return true
		}
		text := script.Text()
		// Unrelated analytics scripts can use syntax outside this literal-only parser.
		if !strings.Contains(text, "appConfig") {
			return true
		}
		tokens, err := scriptTokens(text)
		if err != nil {
			scanErr = err
			return false
		}
		depth := 0
		for i := range tokens {
			if tokenIs(tokens, i, "window") && !tokenIs(tokens, i-1, ".") {
				end := i + 3
				dot := tokenIs(tokens, i+1, ".") && tokenIs(tokens, i+2, "appConfig")
				bracket := tokenIs(tokens, i+1, "[") && i+3 < len(tokens) && tokens[i+2].quoted && tokens[i+2].text == "appConfig" && tokenIs(tokens, i+3, "]")
				if bracket {
					end++
				}
				if (dot || bracket) && end < len(tokens) {
					plain := tokenIs(tokens, end, "=") && !tokenIs(tokens, end+1, "=") && !tokenIs(tokens, end+1, ">")
					modified := configMutation(tokens, end)
					if plain || modified {
						assignments++
						if assignments > 1 {
							scanErr = errors.New("aacg: multiple window.appConfig assignments")
						} else if !plain || bracket || depth != 0 || i > 0 && !tokenIs(tokens, i-1, ";") && !tokenIs(tokens, i-1, "}") {
							scanErr = errors.New("aacg: unsupported window.appConfig assignment; require a standalone top-level literal")
						} else {
							data, key, scanErr = configLiteral(tokens, end+1)
						}
						if scanErr != nil {
							return false
						}
					}
				}
			}
			if !tokens[i].quoted {
				switch tokens[i].text {
				case "{", "[", "(":
					depth++
				case "}", "]", ")":
					depth--
				}
			}
		}
		return true
	})
	if scanErr != nil {
		return nil, scanErr
	}
	if assignments == 0 {
		return nil, errNoConfig
	}
	payload, err := decodeConfig(data, key)
	if err != nil {
		return nil, err
	}
	return configTargets(payload)
}

func configLiteral(tokens []jsToken, i int) (string, string, error) {
	if !tokenIs(tokens, i, "{") {
		return "", "", errors.New("aacg: window.appConfig must be a literal object")
	}
	fields := make(map[string]string)
	for i++; !tokenIs(tokens, i, "}"); {
		if i+2 >= len(tokens) || !tokenIs(tokens, i+1, ":") || !tokens[i+2].quoted || !jsIdentifier(tokens[i]) {
			return "", "", errors.New("aacg: appConfig properties must have literal string values")
		}
		name := tokens[i].text
		if _, exists := fields[name]; exists {
			return "", "", errors.New("aacg: duplicate appConfig property")
		}
		fields[name] = tokens[i+2].text
		i += 3
		if tokenIs(tokens, i, "}") {
			break
		}
		if !tokenIs(tokens, i, ",") {
			return "", "", errors.New("aacg: unsupported appConfig value expression")
		}
		i++
	}
	if i+1 < len(tokens) && !tokenIs(tokens, i+1, ";") {
		return "", "", errors.New("aacg: unsupported appConfig assignment suffix")
	}
	if strings.TrimSpace(fields["data"]) == "" || strings.TrimSpace(fields["key"]) == "" {
		return "", "", errors.New("aacg: appConfig requires nonempty data and key")
	}
	return fields["data"], fields["key"], nil
}

func decodeConfig(data, key string) ([]byte, error) {
	if strings.TrimSpace(data) == "" || strings.TrimSpace(key) == "" || !utf8.ValidString(key) {
		return nil, errors.New("aacg: config data and UTF-8 key must be nonempty")
	}
	encoded, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("aacg: invalid config base64: %w", err)
	}
	if len(encoded) < aes.BlockSize {
		return nil, errors.New("aacg: config has a short IV")
	}
	ciphertext := encoded[aes.BlockSize:]
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("aacg: config ciphertext must be nonempty and block-aligned")
	}
	hash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return nil, fmt.Errorf("aacg: create config cipher: %w", err)
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, encoded[:aes.BlockSize]).CryptBlocks(plaintext, ciphertext)
	padding := int(plaintext[len(plaintext)-1])
	if padding == 0 || padding > aes.BlockSize {
		return nil, errors.New("aacg: invalid config PKCS7 padding")
	}
	for _, b := range plaintext[len(plaintext)-padding:] {
		if int(b) != padding {
			return nil, errors.New("aacg: inconsistent config PKCS7 padding")
		}
	}
	plaintext = plaintext[:len(plaintext)-padding]
	if !utf8.Valid(plaintext) {
		return nil, errors.New("aacg: decrypted config is not valid UTF-8")
	}
	return plaintext, nil
}

type configRoute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Badge string `json:"badge"`
}

func configTargets(payload []byte) ([]Target, error) {
	var config struct {
		Domain json.RawMessage `json:"domain"`
		Backup json.RawMessage `json:"backup_domain"`
	}
	if err := json.Unmarshal(payload, &config); err != nil {
		return nil, fmt.Errorf("aacg: invalid config JSON: %w", err)
	}
	var groups [2][]configRoute
	for i, raw := range []json.RawMessage{config.Domain, config.Backup} {
		if len(raw) == 0 {
			continue
		}
		raw = json.RawMessage(strings.TrimSpace(string(raw)))
		if i == 1 && raw[0] == '{' {
			raw = append(append([]byte{'['}, raw...), ']')
		}
		if raw[0] != '[' {
			return nil, errors.New("aacg: domain must be an array; backup_domain must be an array or object")
		}
		if err := json.Unmarshal(raw, &groups[i]); err != nil {
			return nil, fmt.Errorf("aacg: malformed config route fields: %w", err)
		}
	}
	if len(groups[0])+len(groups[1]) > 32 {
		return nil, errors.New("aacg: config exceeds 32 route entries")
	}
	var targets []Target
	seen := make(map[string]bool)
	for i, group := range groups {
		kind := []string{"primary", "backup"}[i]
		for j, route := range group {
			if strings.TrimSpace(route.Name) == "" || strings.TrimSpace(route.Value) == "" {
				return nil, fmt.Errorf("aacg: %s route %d requires nonempty name and value", kind, j+1)
			}
			u, err := parseURL(route.Value)
			if err != nil {
				return nil, fmt.Errorf("aacg: invalid %s route %d URL: %w", kind, j+1, err)
			}
			normalized := u.String()
			if !seen[normalized] {
				targets = append(targets, Target{Name: route.Name, URL: normalized, Badge: route.Badge, Kind: kind})
				seen[normalized] = true
			}
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("aacg: config contains no explicit routes")
	}
	return targets, nil
}
