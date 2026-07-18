// Package logsanitize strips attacker-controllable control characters from
// values before they reach slog. CodeQL's go/log-injection (CWE-117) flags
// any tainted source flowing into a log statement; even though slog's JSON
// encoder escapes special characters, stripping them at the source keeps
// the log stream one-line-per-event regardless of what the producer sent.
//
// Used wherever an attacker-influenced value reaches slog: the synth RPC
// (unix-socket DAC-gated per ADR-015 but still defense-in-depth), the apid
// domain handler (bearer-token authenticated, custom domain comes from the
// HTTP body), and anywhere else a request-derived identifier reaches a log
// line. Server-generated UUIDs / enum values / OCI digests don't need this —
// call sites there are explicit so a reader sees the boundary.
//
// Empty input is returned as-is (callers usually treat that as a validation
// failure before logging anyway, but the helper stays total).
package logsanitize

import "strings"

// Field strips ASCII control characters from a string intended for a slog
// attribute. Tab (0x09) is preserved because slog's JSON encoder and most
// log readers treat it as benign whitespace. Anything ≤ 0x1F except tab,
// plus DEL (0x7F), is replaced with U+00B7 (middle dot, one UTF-8 codepoint)
// so log readers can spot the sanitization unambiguously.
func Field(s string) string {
	if s == "" {
		return s
	}
	// Use a strings.Builder for clean rune-aware iteration; the previous
	// hand-rolled byte decoder was both slower and not obviously correct.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || r > 0x1F && r != 0x7F {
			b.WriteRune(r)
		} else {
			b.WriteRune('·')
		}
	}
	return b.String()
}

// RedactValue returns a fixed-shape placeholder for a secret VALUE that
// must never reach slog. Used by the apid secrets handlers' defensive log
// sites (key names are public per spec §11 — only values are redacted).
//
// The "<redacted:N>" shape carries the original length so an operator can
// tell "the customer's STRIPE_KEY was 24 bytes" from "it was 4 KB" without
// ever seeing the value. Length is the only signal that escapes.
func RedactValue(s string) string {
	if s == "" {
		return "<redacted:0>"
	}
	// Length is bounded by Limits.SecretValueMaxBytes (32 KB at Scale); the
	// formatted string is always well under that.
	return "<redacted:" + itoa(len(s)) + ">"
}

// itoa is a tiny base-10 formatter. Avoids importing strconv in this hot
// sanitizer path; the function is only ever called with len(s) ≤ 32 KB so
// a 5-element stack buffer is plenty.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
