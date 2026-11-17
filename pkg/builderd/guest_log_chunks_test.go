package builderd

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Kroki feedback 2026-11-17: `du` output printed mid-build was pushed out of
// the 3 KiB tail by the export lines after it.
func TestBoundedGuestBuildLogTailKeepsOutputBeforeLongExport(t *testing.T) {
	export := strings.Repeat("#24 exporting layer sha256:0123456789abcdef 0.4s done\n", 180) // ~10 KiB
	raw := "[build]\n#12 0.5 1.6G\t/usr/lib/node_modules\n#12 0.5 412M\t/opt/kroki\n" + export
	got := boundedGuestBuildLogTail(raw)
	for _, want := range []string{"1.6G\t/usr/lib/node_modules", "412M\t/opt/kroki"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tail lost %q (len %d)", want, len(got))
		}
	}
}

func TestGuestBuildLogChunks(t *testing.T) {
	long := strings.Repeat("é", 2000) // 4000 bytes, one line
	in := strings.Repeat("line of output\n", 500) + long + "\nlast"
	chunks := guestBuildLogChunks(in, guestBuildLogChunkBytes)
	if len(chunks) < 3 {
		t.Fatalf("chunks = %d, want the input split", len(chunks))
	}
	var joined strings.Builder
	for i, c := range chunks {
		if len(c) > guestBuildLogChunkBytes {
			t.Fatalf("chunk %d is %d bytes, want <= %d", i, len(c), guestBuildLogChunkBytes)
		}
		if !strings.HasSuffix(c, "\n") {
			t.Fatalf("chunk %d does not end a line: %q", i, c[len(c)-10:])
		}
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d splits a UTF-8 rune", i)
		}
		joined.WriteString(c)
	}
	// Only the long line gains newlines where it was split.
	if got := strings.ReplaceAll(joined.String(), "\n", ""); got != strings.ReplaceAll(in, "\n", "") {
		t.Fatal("chunks lost or reordered output")
	}
	if guestBuildLogChunks("", 10) != nil {
		t.Fatal("empty input produced chunks")
	}
}
