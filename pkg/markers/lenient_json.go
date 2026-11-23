package markers

import "strings"

// lenientJSON returns s with what a tolerant JSON reader (Node's require
// after npm's BOM strip, Bun's JSONC reader) accepts blanked out: a leading
// UTF-8 byte order mark, // and /* */ comments, and a trailing comma before
// } or ]. Blanked bytes become spaces and newlines stay, so a parse error's
// offset and line still point into the original text. Text inside JSON
// strings is never touched.
func lenientJSON(s string) string {
	b := []byte(strings.TrimPrefix(s, "\xef\xbb\xbf"))
	inString, escaped := false, false
	lastComma := -1 // index of a comma not yet followed by a value byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString, lastComma = true, -1
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			end := strings.Index(string(b[i+2:]), "*/")
			stop := len(b)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			for ; i < stop; i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			i--
		case c == ',':
			lastComma = i
		case c == '}' || c == ']':
			if lastComma >= 0 {
				b[lastComma] = ' '
			}
			lastComma = -1
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		default:
			lastComma = -1
		}
	}
	return string(b)
}
