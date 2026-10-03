package aacg

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// Generated with Node crypto, not the Go test encryptor, to catch mirrored decoder bugs.
const configKnownData = "AAECAwQFBgcICQoLDA0OD0H1fNDrxsZ2eaVQNyez7HJDxAnLDQn1ieXDaFQ0MMUW/Nz3DyK6ymJBi5IwSApQk5XZ1fRfFQj0l2w6tolT3KsFy+Hf/GAO0ClqbCL48F4S2FfzBjWqi3kaaPuv2nb3NhlYRPsQ8/+TSzAo5hQUuIqFyZRxV+uR+FgPN+N71zF/oSgOMFsWNFTHMwaQSxv/RFgOaOywBADCnmzTLJofLglzItXZzgj5kDLg0x8uit8hrYJan9bwYTVE8Fe9+Y2GWCfwrdRI1mgkim2a4Dnzu734Cr+1Y3vXj5Tz3SHytSmp43vkaw/r6wSl4OF/SOigeVE8W8KSPujSUIsUQEZGWgfZ1cCrllHZMUhrjlie7rdj1P8AyjDMDbnXftuSxDXcS5kQvJBpnyg43FpAsrWH9Ec="
const configKnownKey = "synthetic-key-雪"
const configKnownJSON = `{"domain":[{"name":"Primary","value":"https://EXAMPLE.invalid/primary#landing","badge":"Recommended"}],"backup_domain":{"name":"Backup","value":"https://backup.example.invalid/","badge":""},"randomDomain":"1","zz_line":"unused.example.invalid","zz_backup_line":"unused-backup.example.invalid"}`

func configDocument(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func configScript(data, key string) string {
	quotedData, _ := json.Marshal(data)
	quotedKey, _ := json.Marshal(key)
	return "window.appConfig = {data:" + string(quotedData) + ", key:" + string(quotedKey) + "};"
}

func encryptConfigBlocks(t *testing.T, padded []byte, key string) string {
	t.Helper()
	if len(padded) == 0 || len(padded)%aes.BlockSize != 0 {
		t.Fatal("test plaintext must contain complete AES blocks")
	}
	hash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, aes.BlockSize+len(padded))
	for i := range aes.BlockSize {
		encoded[i] = byte(31 - i)
	}
	cipher.NewCBCEncrypter(block, encoded[:aes.BlockSize]).CryptBlocks(encoded[aes.BlockSize:], padded)
	return base64.StdEncoding.EncodeToString(encoded)
}

func encryptConfigJSON(t *testing.T, plaintext []byte) string {
	t.Helper()
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(append([]byte(nil), plaintext...), bytes.Repeat([]byte{byte(padding)}, padding)...)
	return encryptConfigBlocks(t, padded, configKnownKey)
}

func TestConfigKnownAnswer(t *testing.T) {
	plaintext, err := decodeConfig(configKnownData, configKnownKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != configKnownJSON {
		t.Fatalf("decoded config = %q, want %q", plaintext, configKnownJSON)
	}
	targets, err := extractTargets(configDocument(t, "<script>"+configScript(configKnownData, configKnownKey)+"</script>"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{Name: "Primary", URL: "https://example.invalid/primary", Badge: "Recommended", Kind: "primary"},
		{Name: "Backup", URL: "https://backup.example.invalid/", Kind: "backup"},
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %#v, want %#v", targets, want)
	}
}

func TestScriptTokens(t *testing.T) {
	tokens, err := scriptTokens("document /* stale */ . getElementById('go\\x2dhome') // old\n .addEventListener(\"click\", handler);")
	if err != nil {
		t.Fatal(err)
	}
	want := []jsToken{
		{text: "document"}, {text: "."}, {text: "getElementById"}, {text: "("}, {text: "go-home", quoted: true}, {text: ")"},
		{text: "."}, {text: "addEventListener"}, {text: "("}, {text: "click", quoted: true}, {text: ","}, {text: "handler"}, {text: ")"}, {text: ";"},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %#v, want %#v", tokens, want)
	}
}

func TestScriptStringEscapes(t *testing.T) {
	cases := []struct{ script, want string }{
		{`'single\'quote'`, "single'quote"},
		{`"double\"quote"`, "double\"quote"},
		{`'\\\/\b\f\n\r\t\v\0'`, "\\/\b\f\n\r\t\v\x00"},
		{`'\x41\xFF\u96ea\ud834\udd1e'`, "Aÿ雪\U0001d11e"},
		{`'雪\q'`, "雪q"},
		{"'a\\\nb\\\r\nc\\\rd'", "abcd"},
		{"'a\\\u2028b\\\u2029c'", "abc"},
		{`'window.appConfig = {data:"decoy"}; // /*'`, `window.appConfig = {data:"decoy"}; // /*`},
	}
	for _, tc := range cases {
		t.Run(tc.script, func(t *testing.T) {
			tokens, err := scriptTokens(tc.script)
			if err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 1 || !tokens[0].quoted || tokens[0].text != tc.want {
				t.Fatalf("tokens = %#v, want one quoted %q", tokens, tc.want)
			}
		})
	}
}

func TestScriptTokensRejectUnsupportedSyntax(t *testing.T) {
	for _, script := range []string{
		"/* missing end", "'missing end", "'trailing\\", "'raw\nnewline'", "'raw\rnewline'",
		`'\x0'`, `'\xgg'`, `'\u123'`, `'\uzz00'`, `'\u{41}'`, `'\ud800'`, `'\udc00'`, `'\ud800\u0041'`,
		`'\01'`, `'\1'`, `'\8'`, "`template`", "`template ${window.appConfig = {}}`",
		`const decoy = /window.appConfig = {data:'old',key:'old'}/;`, "const ratio = 4 / 2;", `win\u0064ow.appConfig = {};`, "'\xff'",
	} {
		t.Run(script, func(t *testing.T) {
			if _, err := scriptTokens(script); err == nil {
				t.Fatal("expected unsupported or malformed script error")
			}
		})
	}
}

func TestExtractConfigCommentsAndMixedQuotes(t *testing.T) {
	stale := strings.Repeat("window.appConfig = {data:'stale',key:'old'};\n", 1000)
	script := "/*" + stale + "*/\n" + `const decoy = "window.appConfig = {data:'old',key:'old'};";` + "\n" +
		"window /* split */ . appConfig = {\n" +
		"// data: 'stale line',\n" +
		"'data' : '" + configKnownData + "',\n" +
		"/* data: 'stale block', key: 'bad' */\n" +
		`"key" : "synthetic-\x6bey-\u96ea",` + "\n};"
	targets, err := extractTargets(configDocument(t, "<script>"+script+"</script>"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Name != "Primary" || targets[1].Kind != "backup" {
		t.Fatalf("unexpected targets: %#v", targets)
	}
}

func TestExtractConfigAbsent(t *testing.T) {
	assignment := configScript(configKnownData, configKnownKey)
	for name, html := range map[string]string{
		"empty":              "<html></html>",
		"external":           "<script src='https://example.invalid/config.js'>" + assignment + "</script>",
		"empty src":          "<script src>" + assignment + "</script>",
		"HTML comment":       "<!-- <script>" + assignment + "</script> -->",
		"inert script":       "<script type='application/json'>" + assignment + "</script>",
		"comments":           "<script>// " + assignment + "\n/* " + assignment + " */</script>",
		"string decoy":       `<script>const old = 'window.appConfig = {data:"old",key:"old"}';</script>`,
		"quoted punctuation": `<script>'window'; '.'; 'appConfig'; '='; '{}';</script>`,
		"different object":   "<script>other.window.appConfig = {};</script>",
		"comparison":         "<script>window.appConfig === other; window.appConfig == null;</script>",
		"read only":          "<script>window.appConfig && useConfig(); window.appConfig?.key;</script>",
	} {
		t.Run(name, func(t *testing.T) {
			targets, err := extractTargets(configDocument(t, html))
			if !errors.Is(err, errNoConfig) || len(targets) != 0 {
				t.Fatalf("targets = %#v, err = %v; want errNoConfig", targets, err)
			}
		})
	}
	if _, err := extractTargets(nil); !errors.Is(err, errNoConfig) {
		t.Fatalf("nil document error = %v, want errNoConfig", err)
	}
}

func TestExtractConfigIgnoresInertAndExternalScripts(t *testing.T) {
	valid := "<script type='text/javascript'>" + configScript(configKnownData, configKnownKey) + "</script>"
	html := "<script src='ignored.js'>window.appConfig = dynamic();</script>" +
		"<script type='application/ld+json'>{bad JSON and `templates`}</script>" +
		`<script>const path = location.pathname.match(/\/aff-([\w-]+)/);</script>` + valid +
		"<script>const ratio = 4 / 2; console.log(`unrelated`);</script>"
	if _, err := extractTargets(configDocument(t, html)); err != nil {
		t.Fatal(err)
	}
}

func TestExtractConfigRejectsAmbiguousOrDynamic(t *testing.T) {
	valid := configScript(configKnownData, configKnownKey)
	for name, script := range map[string]string{
		"multiple":             valid + valid,
		"multiple scripts":     valid + "</script><script>" + valid,
		"later dynamic":        valid + "window.appConfig = loadConfig();",
		"earlier dynamic":      "window.appConfig = loadConfig();" + valid,
		"function value":       "window.appConfig = loadConfig();",
		"variable data":        "window.appConfig = {data: encrypted, key:'key'};",
		"variable key":         "window.appConfig = {data:'data', key: secret};",
		"concatenation":        "window.appConfig = {data:'a' + 'b', key:'key'};",
		"template value":       "window.appConfig = {data:`value`, key:'key'};",
		"suffix":               "window.appConfig = {data:'data', key:'key'} || old;",
		"spread":               "window.appConfig = {...old, data:'data', key:'key'};",
		"computed key":         "window.appConfig = {['data']:'data', key:'key'};",
		"duplicate key":        "window.appConfig = {data:'data', key:'old', key:'new'};",
		"duplicate data":       "window.appConfig = {data:'old', data:'new', key:'key'};",
		"missing key":          "window.appConfig = {data:'data'};",
		"missing data":         "window.appConfig = {key:'key'};",
		"empty key":            "window.appConfig = {data:'data', key:'  '};",
		"empty data":           "window.appConfig = {data:'', key:'key'};",
		"empty object":         "window.appConfig = {};",
		"unterminated object":  "window.appConfig = {data:'data',key:'key'",
		"unterminated comment": valid + "/*",
		"bracket assignment":   "window['appConfig'] = {data:'data',key:'key'};",
		"compound assignment":  valid + "window.appConfig ||= old;",
		"increment":            valid + "window.appConfig++;",
		"function scope":       "function unused() {" + valid + "}",
		"conditional scope":    "if (false) {" + valid + "}",
		"unbraced conditional": "if (false) " + valid,
		"chained assignment":   "const unused = " + valid,
	} {
		t.Run(name, func(t *testing.T) {
			targets, err := extractTargets(configDocument(t, "<script>"+script+"</script>"))
			if err == nil || errors.Is(err, errNoConfig) || len(targets) != 0 {
				t.Fatalf("targets = %#v, err = %v; want explicit invalid config error", targets, err)
			}
		})
	}
}

func TestScriptUnicodeLineSeparators(t *testing.T) {
	for _, separator := range []string{"\u2028", "\u2029", "\ufeff", "\u00a0"} {
		tokens, err := scriptTokens("left" + separator + "window")
		if err != nil || len(tokens) != 2 || tokens[1].text != "window" {
			t.Fatalf("separator %q: tokens = %#v, err = %v", separator, tokens, err)
		}
	}
	for _, separator := range []string{"\u2028", "\u2029"} {
		script := "// stale assignment" + separator + configScript(configKnownData, configKnownKey)
		if _, err := extractTargets(configDocument(t, "<script>"+script+"</script>")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecodeConfigRejectsInvalidCiphertext(t *testing.T) {
	for name, tc := range map[string]struct{ data, key, message string }{
		"empty data":            {"", configKnownKey, "nonempty"},
		"empty key":             {configKnownData, "", "nonempty"},
		"invalid key UTF8":      {configKnownData, "\xff", "UTF-8"},
		"base64":                {"%%%", configKnownKey, "base64"},
		"base64 padding bits":   {"AB==", configKnownKey, "base64"},
		"short IV":              {base64.StdEncoding.EncodeToString(make([]byte, 15)), configKnownKey, "short IV"},
		"empty ciphertext":      {base64.StdEncoding.EncodeToString(make([]byte, 16)), configKnownKey, "nonempty"},
		"misaligned ciphertext": {base64.StdEncoding.EncodeToString(make([]byte, 33)), configKnownKey, "block-aligned"},
		"wrong key":             {configKnownData, "wrong-synthetic-key", "padding"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeConfig(tc.data, tc.key); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
		})
	}
}

func TestDecodeConfigStrictPadding(t *testing.T) {
	for name, ending := range map[string][]byte{
		"zero": {0}, "over block size": {17}, "over plaintext size": {255}, "inconsistent": {3, 2},
	} {
		t.Run(name, func(t *testing.T) {
			padded := bytes.Repeat([]byte{'x'}, aes.BlockSize)
			copy(padded[len(padded)-len(ending):], ending)
			data := encryptConfigBlocks(t, padded, configKnownKey)
			if _, err := decodeConfig(data, configKnownKey); err == nil || !strings.Contains(err.Error(), "padding") {
				t.Fatalf("error = %v, want padding error", err)
			}
		})
	}
	for size := 0; size < 32; size++ {
		plaintext := bytes.Repeat([]byte{'x'}, size)
		decoded, err := decodeConfig(encryptConfigJSON(t, plaintext), configKnownKey)
		if err != nil || !bytes.Equal(decoded, plaintext) {
			t.Fatalf("size %d: decoded = %q, err = %v", size, decoded, err)
		}
	}
}

func TestDecodeConfigRejectsInvalidUTF8(t *testing.T) {
	data := encryptConfigJSON(t, []byte{0xff, 0xfe})
	if _, err := decodeConfig(data, configKnownKey); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("error = %v, want UTF-8 error", err)
	}
}

func TestConfigTargetsOrderingAndDedupe(t *testing.T) {
	payload := `{"backup_domain":[{"name":"Duplicate backup","value":"https://EXAMPLE.invalid/a#backup","badge":"discard"},{"name":"Backup","value":"http://backup.example.invalid/","badge":"B"}],"domain":[{"name":"First","value":"https://Example.invalid/a#one","badge":"A"},{"name":"Duplicate primary","value":"https://example.invalid/a#two","badge":"discard"},{"name":"Second","value":"https://example.invalid/b"}],"randomDomain":"1","zz_line":"never.example.invalid","zz_backup_line":"never-backup.example.invalid"}`
	targets, err := configTargets([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{Name: "First", URL: "https://example.invalid/a", Badge: "A", Kind: "primary"},
		{Name: "Second", URL: "https://example.invalid/b", Kind: "primary"},
		{Name: "Backup", URL: "http://backup.example.invalid/", Badge: "B", Kind: "backup"},
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %#v, want %#v", targets, want)
	}
}

func TestConfigTargetsBackupShapes(t *testing.T) {
	for _, backup := range []string{
		`{"name":"Backup","value":"https://example.invalid/"}`,
		`[{"name":"Backup","value":"https://example.invalid/"}]`,
	} {
		targets, err := configTargets([]byte(`{"domain":[],"backup_domain":` + backup + `}`))
		if err != nil || len(targets) != 1 || targets[0].Kind != "backup" {
			t.Fatalf("backup %s: targets = %#v, err = %v", backup, targets, err)
		}
	}
}

func TestConfigTargetsRejectsInvalidSchema(t *testing.T) {
	for _, payload := range []string{
		``, `{`, `{} {}`, `null`, `[]`, `"string"`, `{}`, `{"domain":[]}`, `{"domain":[],"backup_domain":[]}`,
		`{"randomDomain":"1","zz_line":"generated.example.invalid","zz_backup_line":"generated-backup.example.invalid"}`,
		`{"domain":{}}`, `{"domain":"bad"}`, `{"domain":null}`, `{"backup_domain":null}`, `{"backup_domain":true}`,
		`{"domain":[null]}`, `{"domain":[{}]}`, `{"domain":["https://example.invalid/"]}`,
		`{"domain":[{"name":123,"value":"https://example.invalid/"}]}`,
		`{"domain":[{"name":"Route","value":123}]}`,
		`{"domain":[{"name":"Route","value":"https://example.invalid/","badge":true}]}`,
		`{"domain":[{"value":"https://example.invalid/"}]}`,
		`{"domain":[{"name":"Route"}]}`,
		`{"domain":[{"name":null,"value":"https://example.invalid/"}]}`,
		`{"domain":[{"name":"  ","value":"https://example.invalid/"}]}`,
		`{"domain":[{"name":"Route","value":" "}]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			data := encryptConfigJSON(t, []byte(payload))
			targets, err := extractTargets(configDocument(t, "<script>"+configScript(data, configKnownKey)+"</script>"))
			if err == nil || errors.Is(err, errNoConfig) || len(targets) != 0 {
				t.Fatalf("targets = %#v, err = %v; want config error", targets, err)
			}
		})
	}
}

func TestConfigTargetsRejectsInvalidURLs(t *testing.T) {
	for _, raw := range []string{
		"relative/path", "//example.invalid/", "ftp://example.invalid/", "javascript:alert(1)", "https:///path",
		"https://user:pass@example.invalid/", "https://user@example.invalid/", "https://bad host.invalid/",
		"https://example.invalid/\nroute", "https://example.invalid/\x00route", "https://example.invalid:bad/",
	} {
		t.Run(raw, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{"domain": []configRoute{{Name: "Route", Value: raw}}})
			if err != nil {
				t.Fatal(err)
			}
			if targets, err := configTargets(payload); err == nil || len(targets) != 0 {
				t.Fatalf("targets = %#v, err = %v; want invalid URL error", targets, err)
			}
		})
	}
}

func TestConfigEntryLimitBeforeDedupe(t *testing.T) {
	for _, count := range []int{32, 33} {
		routes := make([]configRoute, count)
		for i := range routes {
			routes[i] = configRoute{Name: "Duplicate", Value: "https://example.invalid/"}
		}
		payload, err := json.Marshal(map[string]any{"domain": routes[:16], "backup_domain": routes[16:]})
		if err != nil {
			t.Fatal(err)
		}
		targets, err := configTargets(payload)
		if count == 32 {
			if err != nil || len(targets) != 1 || targets[0].Kind != "primary" {
				t.Fatalf("32 entries: targets = %#v, err = %v", targets, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "32") || len(targets) != 0 {
			t.Fatalf("33 entries: targets = %#v, err = %v; want limit error", targets, err)
		}
	}
}
