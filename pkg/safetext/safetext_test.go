package safetext_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/safetext"
)

func TestClean(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ascii passes through", "registry returned 503", "registry returned 503"},
		{"valid multi-byte passes through", "bağlantı zaman aşımı", "bağlantı zaman aşımı"},
		{"empty", "", ""},
		{"lone continuation byte", "ok\xbc", "ok�"},
		{"truncated two-byte rune", "ok\xc3", "ok�"},
		{"nul byte", "a\x00b", "a�b"},
		{"nul and invalid together", "a\x00\xc3b", "a��b"},
		{"other control chars are preserved", "\x1b[32mok\x1b[0m", "\x1b[32mok\x1b[0m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := safetext.Clean(tc.in)
			if got != tc.want {
				t.Fatalf("Clean(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Clean(%q) returned invalid UTF-8", tc.in)
			}
			if strings.IndexByte(got, 0) >= 0 {
				t.Fatalf("Clean(%q) left a NUL byte", tc.in)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		maxBytes int
		want     string
	}{
		{"under the cap", "short", 1024, "short"},
		{"exactly at the cap", "abcd", 4, "abcd"},
		{"ascii over the cap", "abcdef", 4, "abcd"},
		{"zero cap", "abc", 0, ""},
		{"negative cap", "abc", -1, ""},
		// 'ü' is 0xC3 0xBC. A byte slice at 4 would cut between them.
		{"cut lands mid-rune", "abcüdef", 4, "abc"},
		{"cut lands on a rune start", "abcüdef", 5, "abcü"},
		{"cap smaller than the first rune", "üabc", 1, ""},
		{"invalid input is cleaned before cutting", "ab\xc3", 16, "ab�"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := safetext.Truncate(tc.in, tc.maxBytes)
			if got != tc.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tc.in, tc.maxBytes, got, tc.want)
			}
			if len(got) > tc.maxBytes && tc.maxBytes > 0 {
				t.Fatalf("Truncate(%q, %d) returned %d bytes, over the cap", tc.in, tc.maxBytes, len(got))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Truncate(%q, %d) returned invalid UTF-8: %q", tc.in, tc.maxBytes, got)
			}
		})
	}
}

// TestTruncate_NeverExceedsCapOrBreaksUTF8 sweeps every cut position across a
// mixed-width string. This is the property the hand-rolled `s[:n]` form
// violates, and the reason the helper exists.
func TestTruncate_NeverExceedsCapOrBreaksUTF8(t *testing.T) {
	// 1-byte, 2-byte, 3-byte and 4-byte runes so every boundary case is hit.
	const s = "aü→𝄞bç日本語x"
	for n := 0; n <= len(s)+4; n++ {
		got := safetext.Truncate(s, n)
		if n > 0 && len(got) > n {
			t.Fatalf("Truncate(s, %d) returned %d bytes", n, len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("Truncate(s, %d) = %q is not valid UTF-8", n, got)
		}
		if !strings.HasPrefix(s, got) {
			t.Fatalf("Truncate(s, %d) = %q is not a prefix of the input", n, got)
		}
	}
}

func TestTruncateTail(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		maxBytes int
		want     string
	}{
		{"under the cap", "abc", 10, "abc"},
		{"ascii tail", "abcdef", 3, "def"},
		// "aüb" is 4 bytes: a, 0xC3, 0xBC, b. Asking for the last 2 starts at
		// 0xBC — inside 'ü' — so the partial rune is dropped rather than
		// replaced with U+FFFD.
		{"cut lands mid-rune", "aüb", 2, "b"},
		{"cut lands on a rune start", "aüb", 3, "üb"},
		{"whole string", "aüb", 4, "aüb"},
		{"zero", "abc", 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := safetext.TruncateTail(tc.in, tc.maxBytes)
			if got != tc.want {
				t.Fatalf("TruncateTail(%q, %d) = %q, want %q", tc.in, tc.maxBytes, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("TruncateTail(%q, %d) returned invalid UTF-8", tc.in, tc.maxBytes)
			}
			if strings.HasPrefix(got, "�") && !strings.HasPrefix(tc.in, "�") {
				t.Fatalf("TruncateTail(%q, %d) = %q begins with a stray replacement char",
					tc.in, tc.maxBytes, got)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		maxRunes int
		want     string
	}{
		{"under the cap", "abc", 10, "abc"},
		{"exactly at the cap", "abc", 3, "abc"},
		{"ascii over the cap", "abcdef", 3, "abc"},
		{"multi-byte counts runes not bytes", "üüüü", 2, "üü"},
		{"zero", "abc", 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := safetext.TruncateRunes(tc.in, tc.maxRunes); got != tc.want {
				t.Fatalf("TruncateRunes(%q, %d) = %q, want %q", tc.in, tc.maxRunes, got, tc.want)
			}
		})
	}
}

func TestEllipsis(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		maxBytes int
		want     string
	}{
		{"under the cap is untouched", "abc", 10, "abc"},
		{"over the cap gains an ellipsis inside the budget", "abcdefghij", 6, "abc…"},
		{"cap too small for an ellipsis falls back to a plain cut", "abcdef", 2, "ab"},
		// "abcüdef" is 8 bytes; a 7-byte cap leaves 4 for content, and the rune
		// straddling byte 4 is dropped whole rather than split.
		{"never splits a rune before the ellipsis", "abcüdef", 7, "abc…"},
		{"zero", "abc", 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := safetext.Ellipsis(tc.in, tc.maxBytes)
			if got != tc.want {
				t.Fatalf("Ellipsis(%q, %d) = %q, want %q", tc.in, tc.maxBytes, got, tc.want)
			}
			if len(got) > tc.maxBytes {
				t.Fatalf("Ellipsis(%q, %d) returned %d bytes, over the cap", tc.in, tc.maxBytes, len(got))
			}
		})
	}
}

// TestCleanedTextIsJSONMarshalable is the contract the audit-payload call
// sites depend on: once text is cleaned, encoding/json always produces a
// document a jsonb column will accept — including for control characters,
// which json escapes as \u00XX rather than the \xNN that fmt's %q emits.
func TestCleanedTextIsJSONMarshalable(t *testing.T) {
	inputs := []string{
		"plain",
		"\x1b[32mcoloured build output\x1b[0m",
		"control \x01 char",
		"invalid \xc3 sequence",
		"nul \x00 byte",
		"bağlantı zaman aşımına uğradı",
		strings.Repeat("ü", 100),
	}
	for _, in := range inputs {
		payload, err := json.Marshal(struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		}{Action: "abort", Reason: safetext.Clean(in)})
		if err != nil {
			t.Fatalf("json.Marshal(%q): %v", in, err)
		}
		if !json.Valid(payload) {
			t.Fatalf("json.Marshal(%q) produced invalid JSON: %s", in, payload)
		}
	}
}

func TestShellSingleQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "example.com", `'example.com'`},
		{"empty", "", `''`},
		{"spaces are contained", "a b", `'a b'`},
		{"double quotes need no escape inside single quotes", `a"b`, `'a"b'`},
		{"dollar is literal inside single quotes", "$HOME", `'$HOME'`},
		{"a single quote is closed, escaped, and reopened", "a'b", `'a'\''b'`},
		{"the injection CodeQL flagged", `x'; rm -rf /; echo '`, `'x'\''; rm -rf /; echo '\'''`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := safetext.ShellSingleQuote(tc.in); got != tc.want {
				t.Fatalf("ShellSingleQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestShellSingleQuote_RoundTripsThroughARealShell is the assertion that
// matters: whatever goes in must come back out as exactly one argument, with
// its bytes intact. A quoting scheme that merely looks right is not evidence.
func TestShellSingleQuote_RoundTripsThroughARealShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX sh on PATH")
	}
	inputs := []string{
		"example.com",
		"a b",
		"a'b",
		`x'; rm -rf /; echo '`,
		`$(whoami)`,
		"`id`",
		`a"b`,
		"tab\there",
		"ünïcödé.example.com",
		`'`,
		`''`,
		`\`,
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			// printf '%s' <quoted> emits the argument verbatim. If the quoting
			// leaks, sh either errors or produces different bytes.
			script := "printf '%s' " + safetext.ShellSingleQuote(in)
			out, err := exec.Command("sh", "-c", script).Output()
			if err != nil {
				t.Fatalf("sh rejected the quoted form of %q: %v", in, err)
			}
			if string(out) != in {
				t.Fatalf("round trip changed the value: got %q, want %q", out, in)
			}
		})
	}
}

// TestShellSingleQuote_ArgumentCannotSplit pins that a hostile value stays one
// argument rather than becoming several.
func TestShellSingleQuote_ArgumentCannotSplit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX sh on PATH")
	}
	hostile := `a'; echo INJECTED; :'b`
	script := "set -- " + safetext.ShellSingleQuote(hostile) + `; printf '%s' "$#"`
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("sh rejected the quoted form: %v", err)
	}
	if string(out) != "1" {
		t.Fatalf("hostile value produced %s arguments, want 1", out)
	}
}
