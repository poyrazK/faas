package redact_test

import (
	"testing"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/redact"
)

// FuzzApplyKeepsValidUTF8 — redacted output is written to Postgres text
// columns, which reject invalid UTF-8; truncation once split a rune.
func FuzzApplyKeepsValidUTF8(f *testing.F) {
	f.Add("Authorization: Bearer x", "Cookie", "a=b")
	f.Add("user alice@example.com card 4242 4242 4242 4242", "X-Api-Key", "k")
	r := redact.New(64)
	f.Fuzz(func(t *testing.T, s, hk, hv string) {
		out, _ := r.Apply(s)
		if utf8.ValidString(s) && !utf8.ValidString(out) {
			t.Fatalf("valid input produced invalid UTF-8: %q -> %q", s, out)
		}
		_, _ = r.ApplyHeaders(map[string]string{hk: hv})
	})
}
