package logbuf

import (
	"testing"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// A line with invalid UTF-8 (raw bytes, Latin-1 text) used to enter the ring
// as is. The vmmd Logs RPC carries it in a proto3 string, which refuses to
// marshal invalid UTF-8, so every `gregale logs` replay containing the line
// failed until it rotated out of the ring.
func TestRingSanitizesInvalidUTF8(t *testing.T) {
	r := New(1 << 20)
	if _, err := r.Write("stdout", []byte("caf\xe9 \xff\xfe ok\n")); err != nil {
		t.Fatal(err)
	}
	lines := r.Snapshot(1)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	if !utf8.ValidString(lines[0].Line) {
		t.Fatalf("ring kept invalid UTF-8: %q", lines[0].Line)
	}
	if _, err := proto.Marshal(&vmmdpb.LogsResponse{Line: lines[0].Line}); err != nil {
		t.Fatalf("the line cannot cross the Logs RPC: %v", err)
	}
}
