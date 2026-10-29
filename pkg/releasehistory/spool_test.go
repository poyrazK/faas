package releasehistory_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasehistory"
)

func TestSpoolKeepsTheNewestActivations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy", "activations.jsonl")
	started := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	for i := 0; i < releasehistory.SpoolKeep+3; i++ {
		if err := releasehistory.AppendSpool(path, releasehistory.Line{
			ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i), Kind: releasehistory.KindDeploy, ReleaseID: "rel",
			Actor: "operator:ops", Status: releasehistory.StatusSucceeded, Detail: strings.Repeat("x", releasehistory.MaxDetail+10),
			StartedAt: started, FinishedAt: started.Add(time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("spool mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := releasehistory.ReadSpool(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != releasehistory.SpoolKeep {
		t.Fatalf("spool keeps %d lines, want %d", len(lines), releasehistory.SpoolKeep)
	}
	if lines[0].ID != "00000000-0000-0000-0000-000000000003" || lines[len(lines)-1].ID != fmt.Sprintf("00000000-0000-0000-0000-%012d", releasehistory.SpoolKeep+2) {
		t.Fatalf("spool kept %s..%s, want the newest", lines[0].ID, lines[len(lines)-1].ID)
	}
	if len(lines[0].Detail) > releasehistory.MaxDetail || !lines[0].FinishedAt.Equal(started.Add(time.Minute)) {
		t.Fatalf("line = %+v", lines[0])
	}
}

func TestReadSpoolRefusesWhatIsNotOne(t *testing.T) {
	if _, err := releasehistory.ReadSpool(strings.NewReader("{\"id\":\"a\"}\nnot json\n")); err == nil {
		t.Fatal("ReadSpool accepted a line that is not JSON")
	}
}
