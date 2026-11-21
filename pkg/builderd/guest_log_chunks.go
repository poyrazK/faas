package builderd

import (
	"context"
	"strings"
	"unicode/utf8"
)

const (
	// guestBuildLogTailMaxBytes bounds the guest build output kept in the
	// customer's build log. guest-init sends at most half of its 64 KiB
	// log_tail for the build's own output; this keeps most of that.
	guestBuildLogTailMaxBytes = 24 * 1024
	// guestBuildLogChunkBytes bounds one emitted log line: emitBuildLog
	// fans each line out as a PostgreSQL NOTIFY, whose payload limit is
	// 8 KiB after JSON escaping.
	guestBuildLogChunkBytes = 3 * 1024
)

// emitGuestBuildLog writes the bounded guest output tail under header, split
// into NOTIFY-sized chunks on line boundaries.
func (b *Builderd) emitGuestBuildLog(ctx context.Context, buildID, header, raw string) {
	tail := boundedGuestBuildLogTail(raw)
	if tail == "" {
		return
	}
	for i, chunk := range guestBuildLogChunks(tail, guestBuildLogChunkBytes) {
		if i == 0 {
			chunk = header + "\n" + chunk
		}
		b.emitBuildLog(ctx, buildID, chunk)
	}
}

// guestBuildLogChunks splits s into pieces of at most max bytes, each ending
// with a newline. Pieces break between lines; a single line longer than max
// is split inside the line.
func guestBuildLogChunks(s string, max int) []string {
	if s == "" || max <= 0 {
		return nil
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
	}
	for _, line := range strings.SplitAfter(s, "\n") {
		if line == "" {
			continue
		}
		for len(line) > max {
			flush()
			cut := max - 1
			for cut > 1 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			chunks = append(chunks, line[:cut]+"\n")
			line = line[cut:]
		}
		if cur.Len()+len(line) > max {
			flush()
		}
		cur.WriteString(line)
	}
	flush()
	return chunks
}
