package main

import (
	"fmt"
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
