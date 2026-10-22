package outbound

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A close frame's reason must be valid UTF-8 (RFC 6455 §5.5.1); a client may
// fail the connection on a reason cut mid-rune.
func TestWSCloseFrameReasonStaysValidUTF8(t *testing.T) {
	reason := strings.Repeat("a", 122) + "é and more" // é spans bytes 123-124
	frame := wsCloseFrame(1011, reason)
	// Server frame: FIN+opcode, 7-bit length, then the payload (no mask).
	payload := frame[2:]
	if int(frame[1]) != len(payload) || len(payload) > 125 {
		t.Fatalf("close payload length %d (header says %d), want <= 125", len(payload), frame[1])
	}
	got := payload[2:]
	if !utf8.Valid(got) {
		t.Fatalf("close reason is not valid UTF-8: %q", got)
	}
	if string(got) != strings.Repeat("a", 122) {
		t.Fatalf("close reason = %q, want the 122 bytes before the split rune", got)
	}
}
