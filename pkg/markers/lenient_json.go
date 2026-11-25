package markers

import "strings"

// lenientJSON returns s with what the build's package.json readers accept
// (Railpack's hujson, Bun's JSONC reader) blanked out: // and /* */
// comments and a trailing comma before } or ]. They are blanked because
// those readers accept them; npm does not, but its EJSONPARSE error names
// the line in the build log, so the build reports them itself. Blanked bytes become spaces and newlines stay, so a parse error's offset
// and line still point into the original text. Text inside JSON strings is
// never touched. A byte order mark is not stripped: hujson rejects it.
func lenientJSON(s string) string {
	b := []byte(s)
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
