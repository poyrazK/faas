package main

import (
	"fmt"
	"io"
	"strings"
)

// terminalSafe replaces C0 and C1 control characters other than tab, and
// DEL, with a visible \xNN escape before app output reaches a terminal. Apps
// log request data, so an anonymous visitor could plant escape sequences
// (OSC 52 clipboard writes, disguised OSC 8 links, title and screen
// manipulation) that ran in whoever tailed the logs (bug hunt 2026-10-28
// H32). Unicode bidirectional controls become a \uNNNN escape: a planted
// right-to-left override visually reorders the rest of the line, so a log line
// could read as something it is not (ADR-20261110090000). --json output keeps
// the raw bytes.
func terminalSafe(s string) string {
	if !strings.ContainsFunc(s, unsafeTerminalRune) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case bidiControlRune(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		case unsafeTerminalRune(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafeTerminalRune(r rune) bool {
	return r != '\t' && (r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f) || bidiControlRune(r)
}

// bidiControlRune reports the Unicode bidirectional formatting characters:
// the embeddings and overrides (U+202A..U+202E), the isolates
// (U+2066..U+2069) and the implicit marks (U+200E, U+200F, U+061C).
func bidiControlRune(r rune) bool {
	return r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0x200e || r == 0x200f || r == 0x061c
}

// terminalSafeLines splits one build/deploy log entry into terminal-safe
// lines. A guest build chunk can hold several lines ("[guest build
// output]\nnode toolchain HTTPS preflight: …\n"), and terminalSafe turned
// each line break into a literal \x0a that ran the whole explanation together
// (CLI journey 2026-11-05, #6633). LF and CRLF line ends become line breaks;
// every other control character is still escaped, and a literal backslash
// sequence in the text is left alone.
func terminalSafeLines(entry string) []string {
	entry = strings.TrimRight(entry, "\r\n")
	lines := strings.Split(entry, "\n")
	for i, line := range lines {
		lines[i] = terminalSafe(strings.TrimSuffix(line, "\r"))
	}
	return lines
}

// printTerminalSafeLines writes entry as terminal-safe lines, each preceded
// by prefix.
func printTerminalSafeLines(w io.Writer, prefix, entry string) error {
	for _, line := range terminalSafeLines(entry) {
		if _, err := fmt.Fprintln(w, prefix+line); err != nil {
			return err
		}
	}
	return nil
}
