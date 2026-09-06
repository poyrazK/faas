package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTLSCutoverState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tls-cutover.state")
	if err := os.WriteFile(path, []byte("state=rolled_back\nrun_id=20260906T120000Z\nupdated_at=2026-09-06T12:01:00Z\noperator=ops@example.com\nmessage=rollback verified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTLSCutoverState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "rolled_back" || got.RunID != "20260906T120000Z" || got.Operator != "ops@example.com" {
		t.Fatalf("state = %+v", got)
	}
}

func TestReadTLSCutoverStateMissing(t *testing.T) {
	_, err := ReadTLSCutoverState(filepath.Join(t.TempDir(), "missing"))
	if !IsMissingTLSCutoverState(err) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}
