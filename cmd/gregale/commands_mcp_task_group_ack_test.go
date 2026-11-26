package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestMCPTaskGroupAckPersistsPrivateMarker(t *testing.T) {
	dir := t.TempDir()
	id, groupID := strings.Repeat("a", 64), strings.Repeat("b", 64)
	writeMCPTaskGroupTestFile(t, dir, "group.json", fmt.Sprintf(
		`{"version":1,"id":%q,"endpointSha256":%q,"members":[{"submissionKey":"019ab2ce-217c-7abc-89ab-123456789abc"}]}`,
		groupID, groupID))
	args := []string{"mcp", "client", "group", "ack", "--group", dir, "--update-id", id}
	marker := filepath.Join(dir, "ack-"+id)
	var first os.FileInfo
	for range 2 {
		out, restore := main.CaptureStdout(t)
		code := main.Run(args)
		restore()
		want := fmt.Sprintf("{\"updateId\":%q,\"acknowledged\":true}", id)
		if code != 0 || strings.Join(strings.Fields(out.String()), "") != want {
			t.Fatalf("acknowledgement exit=%d output=%s", code, out.String())
		}
		info, err := os.Lstat(marker)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != 0 {
			t.Fatalf("acknowledgement marker must be an empty private regular file: info=%v err=%v", info, err)
		}
		if first != nil && !os.SameFile(first, info) {
			t.Fatal("repeated acknowledgement replaced an existing marker")
		}
		first = info
	}

	badID := strings.Repeat("c", 64)
	badMarker := filepath.Join(dir, "ack-"+badID)
	if err := os.Symlink(marker, badMarker); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = badID
	stderr, restore := main.CaptureStderr(t)
	code := main.Run(args)
	restore()
	if code == 0 || !strings.Contains(stderr.String(), "invalid MCP Task group acknowledgement marker") {
		t.Fatalf("symlink acknowledgement exit=%d stderr=%s", code, stderr.String())
	}
	if target, err := os.Readlink(badMarker); err != nil || target != marker {
		t.Fatalf("symlink changed: target=%q err=%v", target, err)
	}
}
