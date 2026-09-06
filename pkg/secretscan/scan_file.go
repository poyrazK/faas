// ScanFile is the generic entry point for scanning a single file's bytes
// without requiring KEY=VALUE shape. v1 (PR #862) shipped ScanEnvContent,
// which assumes the .env parser shape (KEY=VALUE\n). For source-tree
// scanning (cmd/apid/secretscan.go, --secret-scan=source-tree) we need a
// shape-agnostic scan: a PEM-armour line, a hard-coded API key in
// index.ts, or a Stripe literal embedded in a JSON config string.
//
// The logic:
//
//   - Iterate lines (same zero-alloc bytes.IndexByte loop as scan.go).
//   - For each line, if it matches `^[A-Za-z_][A-Za-z0-9_]*=...` (with a
//     lenient first-char range — JSON keys are quoted so we also accept
//     `"KEY": "VALUE"` shapes via the same regex by stripping quotes), run
//     matchValue against the (key, value) pair.
//   - Otherwise, treat the whole line as a value candidate so PEM-armour
//     blocks (which have no KEY= prefix on each line) and similar
//     value-only patterns still fire.
//
// The function does NOT do binary-skip. The caller (cmd/apid/secretscan.go
// or cmd/gregale/pack.go) runs IsTextFile first; passing binary content
// here will likely produce a low-signal but not catastrophic result. This
// is the same delegation shape as ScanEnvContent.
package secretscan

import (
	"bytes"
)

// ScanFile scans a single file's bytes for secret-shaped lines. Returns
// the findings in file-order. The path is recorded on each Finding so the
// caller can render File:Line in the stderr / audit output.
//
// Same armoured-key dedup as ScanEnvContent (review finding #3):
// a single PEM block produces one finding on the BEGIN line, not
// one per base64 body line.
func ScanFile(path string, data []byte) []Finding {
	var out []Finding
	inPEMBlock := false
	for lineNo := 1; ; lineNo++ {
		i := bytes.IndexByte(data, '\n')
		var line []byte
		if i < 0 {
			line = data
			data = nil
		} else {
			line = data[:i]
			data = data[i+1:]
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		// PEM-block state update before the match pass so the
		// -----BEGIN line itself still scans.
		if bytes.Contains(line, []byte("-----BEGIN ")) && bytes.Contains(line, []byte("PRIVATE KEY")) {
			inPEMBlock = true
		}
		if bytes.Contains(line, []byte("-----END ")) && bytes.Contains(line, []byte("PRIVATE KEY")) {
			inPEMBlock = false
		}
		out = scanOneLine(path, lineNo, line, out, inPEMBlock)
		if len(data) == 0 {
			return out
		}
	}
}

// scanOneLine routes one line through either matchValue (if the line
// looks like a key=value pair) or matchValue-with-empty-key (if it's a
// whole-line candidate for value-only patterns like private_key_block).
//
// Both call paths land in matchValue because the value-only patterns
// (KeyHint=="") are already designed to scan a raw value string.
//
// Separator detection order: `=` (env / shell), `:` (YAML / JSON / JS
// object literal — the colon must be followed by space or end-of-line
// so we don't trip on URL-shaped values like
// `https://x.com?key=...`). `=` is preferred when both are present so an
// `API_KEY=foo:bar` line lands on the key= side.
//
// inPEMBlock suppresses the entropy fallback on body lines of an
// armoured-key block (review finding #3).
func scanOneLine(path string, lineNo int, line []byte, out []Finding, inPEMBlock bool) []Finding {
	// Skip blanks and shell-style comments. Comments are not scanned
	// even if they contain a real-looking token, because the token is by
	// definition not a secret if it's commented out.
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] == '#' {
		return out
	}
	// Try the env-parser shape first: KEY=VALUE.
	if key, value, ok := splitKeyValue(line, '='); ok {
		if f := matchSourceValue(path, lineNo, key, value, inPEMBlock); f != nil {
			out = append(out, *f)
		}
		return out
	}
	// Then YAML/JSON/JS-object shape: KEY: VALUE. We only accept a colon
	// if the KEY on the left is key-shaped — that filter keeps URL values
	// from accidentally splitting on the host:port boundary.
	if key, value, ok := splitKeyValueColon(trimmed); ok {
		if f := matchSourceValue(path, lineNo, key, value, inPEMBlock); f != nil {
			out = append(out, *f)
		}
		return out
	}
	// Whole-line value candidate (PEM armour, base64 blob, etc.).
	if f := matchSourceValue(path, lineNo, "", trimmed, inPEMBlock); f != nil {
		out = append(out, *f)
	}
	return out
}

// splitKeyValue extracts KEY=VALUE from a line. Returns ok=false if the
// left side is not key-shaped or the separator is too far to the right.
//
// Strategy: try the FIRST `=` first (the env-parser shape). If the left
// side isn't key-shaped (e.g. JS `const FOO = "..."`), try the LAST `=`
// on the line — the identifier sits between the keyword and the operator.
// If neither candidate produces a key-shaped left side, give up.
func splitKeyValue(line []byte, sep byte) (string, []byte, bool) {
	first := bytes.IndexByte(line, sep)
	if first <= 0 {
		return "", nil, false
	}
	if key, value, ok := tryKeyEq(line, first); ok {
		return key, value, ok
	}
	last := bytes.LastIndexByte(line, sep)
	if last == first {
		return "", nil, false
	}
	if key, value, ok := tryKeyEq(line, last); ok {
		return key, value, ok
	}
	return "", nil, false
}

// tryKeyEq validates the (key, value) pair at a specific offset and
// returns them on success.
func tryKeyEq(line []byte, eq int) (string, []byte, bool) {
	if eq > 64 {
		return "", nil, false
	}
	key := bytes.TrimSpace(line[:eq])
	key = unquoteKey(key)
	// JS declaration shapes: `const FOO = ...`, `let FOO = ...`,
	// `var FOO = ...`, `export const FOO = ...`, etc. The actual
	// identifier is the LAST whitespace-separated word on the left of
	// `=`. We take it only when the rest of the left side is one of
	// those keywords or empty; if not, we still try the raw trimmed
	// left so env-shaped lines like `PATH=/usr/bin` keep working.
	if i := lastWhitespace(key); i >= 0 {
		prefix := key[:i]
		if isJSDecorationPrefix(prefix) {
			key = key[i+1:]
		}
	}
	if !isKeyShaped(key) {
		return "", nil, false
	}
	value := line[eq+1:]
	return string(key), value, true
}

// isJSDecorationPrefix reports whether prefix is a JS declaration chain
// that we'd expect immediately before a key= binding.
func isJSDecorationPrefix(prefix []byte) bool {
	for _, kw := range []string{"const", "let", "var", "export"} {
		if bytesEqualFold(prefix, []byte(kw)) || hasOnlyJSDecorators(prefix) {
			return true
		}
	}
	return false
}

// hasOnlyJSDecorators reports whether prefix is a sequence of JS
// declaration keywords separated by spaces (e.g. "export const").
func hasOnlyJSDecorators(prefix []byte) bool {
	for _, word := range bytesSplitSpaces(prefix) {
		switch string(word) {
		case "const", "let", "var", "export":
		default:
			return false
		}
	}
	return len(prefix) > 0
}

// lastWhitespace returns the index of the last whitespace byte in b, or
// -1 if there is none.
func lastWhitespace(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == ' ' || b[i] == '\t' {
			return i
		}
	}
	return -1
}

// bytesSplitSpaces splits b on ASCII spaces and tabs.
func bytesSplitSpaces(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == ' ' || c == '\t' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// bytesEqualFold is a tiny ASCII-case-insensitive compare (we only
// compare against known-lowercase bytes).
func bytesEqualFold(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// splitKeyValueColon is the JSON / YAML / JS-object variant: KEY: VALUE
// where KEY is key-shaped and the value starts after `: ` or `:`. We
// only accept a colon if the LEFT side is key-shaped so URL values like
// `https://example.com:8080/x` are never mis-split.
//
// Strategy mirrors splitKeyValue: try the LAST `:` on the line first,
// because JSON/YAML/JS-object literals put the separator near the END of
// the key (`{"KEY": "value"}`). Falling back to the first `:` is rarely
// useful since it usually lands inside a `{` or array index.
func splitKeyValueColon(line []byte) (string, []byte, bool) {
	// Try the LAST colon first — JSON's `{"KEY": VALUE}` shape puts it
	// right before the value.
	last := bytes.LastIndexByte(line, ':')
	if last > 0 {
		if key, value, ok := tryKeyColon(line, last); ok {
			return key, value, ok
		}
	}
	// Then the first — catches YAML's `KEY: VALUE` shape (single colon).
	first := bytes.IndexByte(line, ':')
	if first > 0 && first != last {
		if key, value, ok := tryKeyColon(line, first); ok {
			return key, value, ok
		}
	}
	return "", nil, false
}

func tryKeyColon(line []byte, colon int) (string, []byte, bool) {
	if colon > 64 {
		return "", nil, false
	}
	key := bytes.TrimSpace(line[:colon])
	key = unquoteKey(key)
	// Strip a leading object-literal `{` so JSON `{"KEY": VALUE}` works.
	// We don't need to balance braces — the left side is bounded by the
	// colon we already located.
	if len(key) > 0 && key[0] == '{' {
		key = bytes.TrimSpace(key[1:])
		key = unquoteKey(key)
	}
	if !isKeyShaped(key) {
		return "", nil, false
	}
	value := bytes.TrimSpace(line[colon+1:])
	return string(key), value, true
}

// unquoteKey strips surrounding ASCII quotes (' or ") from a key. Returns
// the input unchanged if the quote pair is mismatched or the key is too
// short.
func unquoteKey(b []byte) []byte {
	if len(b) >= 2 && (b[0] == '"' && b[len(b)-1] == '"') ||
		(b[0] == '\'' && b[len(b)-1] == '\'') {
		return b[1 : len(b)-1]
	}
	return b
}

// isKeyShaped reports whether b looks like an env-var key: [A-Za-z_]
// followed by [A-Za-z0-9_]*. JSON keys often include dots
// ("stripe.secret") and dashes ("api-key"); we accept those too.
func isKeyShaped(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for i, c := range b {
		switch {
		case c == '_':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case i > 0 && c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == '-'):
		default:
			return false
		}
	}
	return true
}
