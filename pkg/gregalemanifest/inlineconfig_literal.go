package gregalemanifest

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// literalLang selects the comment syntax and keyword spelling of the
// literal subset parseLiteral accepts (ADR-20261107193000).
type literalLang int

const (
	langJS literalLang = iota
	langPython
)

// literalMaxDepth bounds nesting so a hostile file cannot exhaust the stack.
const literalMaxDepth = 32

// literalParser reads one literal value (object/dict, array/list, string,
// number, boolean, null/None) from src starting at pos. It accepts comments
// and trailing commas and rejects every non-literal expression, so a config
// that would differ between deploys is never silently dropped.
type literalParser struct {
	src  string
	pos  int
	lang literalLang
}

// literalError carries the 1-based line of the offending token.
type literalError struct {
	Line int
	Msg  string
}

func (e *literalError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

func (p *literalParser) fail(format string, args ...any) error {
	return &literalError{Line: 1 + strings.Count(p.src[:min(p.pos, len(p.src))], "\n"), Msg: fmt.Sprintf(format, args...)}
}

func (p *literalParser) skipSpace() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case p.lang == langJS && strings.HasPrefix(p.src[p.pos:], "//"):
			p.skipLine()
		case p.lang == langJS && strings.HasPrefix(p.src[p.pos:], "/*"):
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				p.pos = len(p.src)
				return
			}
			p.pos += end + 4
		case p.lang == langPython && c == '#':
			p.skipLine()
		case p.lang == langPython && c == '\\' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '\n':
			p.pos += 2
		default:
			return
		}
	}
}

func (p *literalParser) skipLine() {
	if nl := strings.IndexByte(p.src[p.pos:], '\n'); nl >= 0 {
		p.pos += nl + 1
		return
	}
	p.pos = len(p.src)
}

func (p *literalParser) peek() byte {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

// value parses one literal.
func (p *literalParser) value(depth int) (any, error) {
	if depth > literalMaxDepth {
		return nil, p.fail("config nests deeper than %d levels", literalMaxDepth)
	}
	switch c := p.peek(); {
	case c == 0:
		return nil, p.fail("config ends before its value is complete")
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"' || c == '\'' || (c == '`' && p.lang == langJS):
		return p.str()
	case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
		return p.number()
	case isIdentStart(rune(c)):
		start := p.pos
		word := p.ident()
		switch {
		case p.lang == langJS && word == "true", p.lang == langPython && word == "True":
			return true, nil
		case p.lang == langJS && word == "false", p.lang == langPython && word == "False":
			return false, nil
		case p.lang == langJS && (word == "null" || word == "undefined"), p.lang == langPython && word == "None":
			return nil, nil
		}
		p.pos = start
		return nil, p.fail("config must contain only literal values; %q is an expression (put values that change between deploys in env or secrets)", p.expressionSnippet())
	default:
		return nil, p.fail("config must contain only literal values; found %q", p.expressionSnippet())
	}
}

// expressionSnippet returns the offending text up to a delimiter, for errors.
func (p *literalParser) expressionSnippet() string {
	end := p.pos
	for end < len(p.src) && end-p.pos < 40 && !strings.ContainsRune(",}]\n", rune(p.src[end])) {
		end++
	}
	return strings.TrimSpace(p.src[p.pos:end])
}

func (p *literalParser) object(depth int) (map[string]any, error) {
	p.pos++ // {
	out := map[string]any{}
	for {
		c := p.peek()
		if c == '}' {
			p.pos++
			return out, nil
		}
		if c == '.' && strings.HasPrefix(p.src[p.pos:], "...") || c == '*' && strings.HasPrefix(p.src[p.pos:], "**") {
			return nil, p.fail("config must contain only literal values; spreads are not allowed")
		}
		key, err := p.objectKey()
		if err != nil {
			return nil, err
		}
		if p.peek() != ':' {
			return nil, p.fail("expected ':' after key %q", key)
		}
		p.pos++
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			return nil, p.fail("duplicate key %q", key)
		}
		out[key] = v
		if !p.listSeparator('}') {
			return nil, p.fail("expected ',' or '}' after the value of %q", key)
		}
	}
}

func (p *literalParser) objectKey() (string, error) {
	c := p.peek()
	switch {
	case c == '"' || c == '\'' || (c == '`' && p.lang == langJS):
		v, err := p.str()
		if err != nil {
			return "", err
		}
		return v.(string), nil
	case p.lang == langJS && isIdentStart(rune(c)):
		return p.ident(), nil
	case c == '[' && p.lang == langJS:
		return "", p.fail("config must contain only literal values; computed keys are not allowed")
	case p.lang == langPython && isIdentStart(rune(c)):
		return "", p.fail("dict keys must be quoted strings; %q is a variable", p.expressionSnippet())
	default:
		return "", p.fail("expected a key, found %q", p.expressionSnippet())
	}
}

func (p *literalParser) array(depth int) ([]any, error) {
	p.pos++ // [
	out := []any{}
	for {
		if p.peek() == ']' {
			p.pos++
			return out, nil
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if !p.listSeparator(']') {
			return nil, p.fail("expected ',' or ']' in a list")
		}
	}
}

// listSeparator consumes a ',' (or nothing before the closing byte) and
// reports whether the list continues validly.
func (p *literalParser) listSeparator(closing byte) bool {
	switch p.peek() {
	case ',':
		p.pos++
		return true
	case closing:
		return true
	}
	return false
}

func (p *literalParser) str() (any, error) {
	quote := p.src[p.pos]
	if p.lang == langPython && strings.HasPrefix(p.src[p.pos:], strings.Repeat(string(quote), 3)) {
		return nil, p.fail("triple-quoted strings are not supported in config")
	}
	p.pos++
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == quote:
			p.pos++
			return b.String(), nil
		case c == '\n' && quote != '`':
			return nil, p.fail("string is not closed before the end of the line")
		case c == '$' && quote == '`' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '{':
			return nil, p.fail("config must contain only literal values; template substitutions are not allowed")
		case c == '\\':
			if p.pos+1 >= len(p.src) {
				return nil, p.fail("string ends in a backslash")
			}
			p.pos++
			switch e := p.src[p.pos]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '0':
				b.WriteByte(0)
			case 'u':
				if p.pos+4 >= len(p.src) {
					return nil, p.fail("short \\u escape")
				}
				n, err := strconv.ParseUint(p.src[p.pos+1:p.pos+5], 16, 32)
				if err != nil {
					return nil, p.fail("invalid \\u escape")
				}
				b.WriteRune(rune(n))
				p.pos += 4
			default:
				b.WriteByte(e)
			}
			p.pos++
		default:
			r, size := utf8.DecodeRuneInString(p.src[p.pos:])
			b.WriteRune(r)
			p.pos += size
		}
	}
	return nil, p.fail("string is not closed")
}

func (p *literalParser) number() (any, error) {
	start := p.pos
	for p.pos < len(p.src) && strings.ContainsRune("+-0123456789._eExXabcdefABCDEF", rune(p.src[p.pos])) {
		p.pos++
	}
	text := strings.ReplaceAll(p.src[start:p.pos], "_", "")
	if n, err := strconv.ParseInt(text, 0, 64); err == nil {
		return int(n), nil
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		return f, nil
	}
	p.pos = start
	return nil, p.fail("%q is not a number", p.expressionSnippet())
}

func (p *literalParser) ident() string {
	start := p.pos
	for p.pos < len(p.src) {
		r, size := utf8.DecodeRuneInString(p.src[p.pos:])
		if !isIdentPart(r) {
			break
		}
		p.pos += size
	}
	return p.src[start:p.pos]
}

// kwargs parses a Python call's keyword arguments after its '(' up to and
// including ')': `cron="0 8 * * *", ram_mb=512`.
func (p *literalParser) kwargs() (map[string]any, error) {
	out := map[string]any{}
	for {
		c := p.peek()
		if c == ')' {
			p.pos++
			return out, nil
		}
		if !isIdentStart(rune(c)) {
			return nil, p.fail("decorator arguments must be keyword arguments like cron=\"0 8 * * *\"")
		}
		key := p.ident()
		if p.peek() != '=' {
			return nil, p.fail("decorator arguments must be keyword arguments; %q has no '='", key)
		}
		p.pos++
		v, err := p.value(1)
		if err != nil {
			return nil, err
		}
		if _, dup := out[key]; dup {
			return nil, p.fail("duplicate argument %q", key)
		}
		out[key] = v
		if !p.listSeparator(')') {
			return nil, p.fail("expected ',' or ')' after argument %q", key)
		}
	}
}

func isIdentStart(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return isIdentStart(r) || unicode.IsDigit(r) }
