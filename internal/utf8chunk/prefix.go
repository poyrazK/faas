// Package utf8chunk splits text without replacing valid UTF-8 that straddles
// transport frames. Invalid complete bytes retain encoding/json's usual policy.
package utf8chunk

import "unicode/utf8"

// Prefix returns a bounded prefix ending on a rune boundary. Unless final is
// set, an incomplete trailing rune is held for the next transport frame.
func Prefix(data []byte, limit int, final bool) int {
	end := min(len(data), limit)
	if end < len(data) {
		for end > 0 && !utf8.RuneStart(data[end]) {
			end--
		}
		if end == 0 {
			end = min(len(data), limit)
		}
	}
	if !final || end < len(data) {
		for start := max(0, end-utf8.UTFMax+1); start < end; start++ {
			if utf8.RuneStart(data[start]) && !utf8.FullRune(data[start:end]) {
				return start
			}
		}
	}
	return end
}
