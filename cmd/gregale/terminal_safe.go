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
// H32). --json output keeps the raw bytes.
func terminalSafe(s string) string {
	if !strings.ContainsFunc(s, unsafeTerminalRune) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		if unsafeTerminalRune(r) {
			fmt.Fprintf(&b, `\x%02x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func unsafeTerminalRune(r rune) bool {
	return r != '\t' && (r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f)
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
