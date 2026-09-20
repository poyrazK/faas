// Package safetext normalizes free text before it crosses a serialization
// boundary — a Postgres value, a pg_notify payload, or a JSON document.
//
// Three properties of PostgreSQL make raw Go strings unsafe to write directly:
//
//   - A `text` column on a UTF8 database rejects invalid byte sequences with
//     SQLSTATE 22021 (`invalid byte sequence for encoding "UTF8"`).
//   - A `text` column rejects the NUL byte specifically, even though NUL is
//     valid UTF-8 (`null character not permitted`).
//   - A `jsonb` column rejects anything that is not valid JSON with SQLSTATE
//     22P02 (`invalid input syntax for type json`).
//
// Go strings carry none of these guarantees. `err.Error()` can contain
// arbitrary bytes from a registry response, guest output, or an upstream API
// body; customer input can contain any code point the JSON spec allows.
//
// A shell command line is the same kind of boundary: inside single quotes
// every byte is literal except the single quote, which ends the quoted run.
//
// Three rules follow, and this package exists so none has to be re-derived at
// each call site:
//
//  1. Never truncate free text with a byte slice. `s[:n]` splits multi-byte
//     runes and produces invalid UTF-8. Use Truncate.
//  2. Never build JSON with fmt verbs. `%q` is strconv.Quote, not a JSON
//     encoder: it emits `\xNN` escapes that JSON has no grammar for. Use
//     encoding/json on a struct, or Clean if a bare string must be embedded.
//  3. Never interpolate a value into hand-written shell quotes. Use
//     ShellSingleQuote, which supplies the quotes itself.
package safetext

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// JSONObject encodes v as a JSON document for a jsonb column, a pg_notify
// payload, or an HTTP response body. It is the replacement for building JSON
// with fmt and the %q verb.
//
// %q is strconv.Quote, not a JSON encoder. For a control byte it emits \xNN,
// an escape JSON has no grammar for, and the result is rejected by a jsonb
// column with SQLSTATE 22P02 and by any conforming parser. encoding/json
// emits \u00XX for the same byte and replaces invalid UTF-8 with U+FFFD, so
// its output is always parseable.
//
// v must be a value whose marshaling cannot fail — a struct, map or slice of
// strings, numbers and bools, which covers every payload in this codebase.
// Channels, funcs, cyclic structures and custom Marshalers that return errors
// do not qualify. For those, call json.Marshal directly and handle the error.
//
// The unreachable error branch returns an empty object rather than an error,
// because a well-formed `{}` is always safe for the destinations above while
// a returned error would be discarded at every call site.
func JSONObject(v any) []byte {
	encoded, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return encoded
}

// replacement substitutes any byte sequence that Postgres will not accept.
// U+FFFD REPLACEMENT CHARACTER is the Unicode-sanctioned stand-in and is what
// strings.ToValidUTF8 and encoding/json both converge on.
const replacement = "�"

// ShellSingleQuote returns s as a single POSIX shell word, wrapped in single
// quotes and safe to paste into a command line.
//
// Inside single quotes a POSIX shell treats every byte literally except the
// single quote itself, which ends the quoted run. The standard escape is to
// close the run, emit a backslash-escaped quote, and reopen. Anything that
// interpolates a value into a hand-built `'...'` needs this; JSON encoding
// does not help, because ' requires no escaping in JSON and so survives
// json.Marshal untouched.
//
// This matters wherever the platform prints a command for an operator to run.
// A value carrying a quote does not merely render oddly — it terminates the
// argument and the remainder is parsed as shell.
func ShellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(Clean(s), "'", `'\''`) + "'"
}

// Clean returns s with every byte sequence Postgres rejects replaced: invalid
// UTF-8 and the NUL byte. The result is always safe to write to a `text`
// column and always safe to hand to encoding/json.
//
// Clean does not remove other control characters. They are legal in a Postgres
// text column and encoding/json escapes them correctly as \u00XX, so stripping
// them would silently discard meaning (an ANSI-coloured build log line, for
// example) for no safety gain.
func Clean(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", replacement)
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, replacement)
	}
	return s
}

// Truncate returns s cleaned by Clean and then reduced to at most maxBytes
// bytes, never splitting a rune. It is the replacement for `if len(s) > n { s
// = s[:n] }` on any string that is not known to be pure ASCII.
//
// The bound is in bytes, not runes, because the limits it protects are byte
// limits: Postgres column widths and the ~8 KB pg_notify payload ceiling. A
// caller that wants a rune bound should use TruncateRunes.
//
// maxBytes <= 0 returns the empty string.
func Truncate(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	s = Clean(s)
	if len(s) <= maxBytes {
		return s
	}
	// Walk back to the start of the rune that straddles the cut. A rune is at
	// most utf8.UTFMax bytes, so this loop runs at most 3 times on valid
	// UTF-8 — and s is valid here because Clean already ran.
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// TruncateTail returns the LAST maxBytes bytes of s, cleaned by Clean and
// advanced forward to the next rune boundary so the result never begins
// mid-rune. Use it to keep the end of a log or error tail.
//
// The naive form, s[len(s)-n:], starts inside a rune whenever the cut lands
// there. Callers that follow it with strings.ToValidUTF8 get a valid string,
// but one that begins with a stray U+FFFD; this drops the partial rune
// instead.
//
// maxBytes <= 0 returns the empty string.
func TruncateTail(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	s = Clean(s)
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// TruncateRunes returns s cleaned by Clean and then reduced to at most
// maxRunes runes. Use it for text whose limit is a human-facing character
// count (a display label, a CLI column) rather than a storage bound.
//
// maxRunes <= 0 returns the empty string.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	s = Clean(s)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// Ellipsis returns s reduced to at most maxBytes bytes with a trailing
// horizontal-ellipsis rune when anything was removed. The ellipsis is included
// in the budget, so the result never exceeds maxBytes.
//
// This exists because the obvious hand-rolled form, `s[:n] + "…"`, is doubly
// wrong: it splits a rune at n AND then overruns the caller's own limit by the
// three bytes of the ellipsis.
func Ellipsis(s string, maxBytes int) string {
	const ellipsis = "…" // U+2026, 3 bytes in UTF-8
	if maxBytes <= 0 {
		return ""
	}
	s = Clean(s)
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= len(ellipsis) {
		return Truncate(s, maxBytes)
	}
	return Truncate(s, maxBytes-len(ellipsis)) + ellipsis
}
