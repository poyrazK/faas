package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
)

func TestRecoveryCommands(t *testing.T) {
	dir := t.TempDir()
	source, backup, restored := filepath.Join(dir, "source"), filepath.Join(dir, "backup"), filepath.Join(dir, "restored")
	s, err := objectnode.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Put(t.Context(), "test-bucket", "key", strings.NewReader("body"), 4, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	var id string
	for _, args := range [][]string{
		{"backup", "--data-dir", source, "--backup-dir", backup},
		{"verify", "--backup-dir", backup},
		{"restore", "--backup-dir", backup, "--data-dir", restored},
	} {
		var output bytes.Buffer
		if err = run(t.Context(), args, &output); err != nil {
			t.Fatal(args[0], err)
		}
		var info objectnode.BackupInfo
		if err = json.Unmarshal(output.Bytes(), &info); err != nil || info.ID == "" || id != "" && info.ID != id {
			t.Fatal(output.String(), err)
		}
		id = info.ID
	}
	if err = os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	node, err := objectnode.Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	object, file, err := node.Get("test-bucket", "key")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if object.Size != 4 {
		t.Fatal(object)
	}
}

func TestRecoveryCommandArguments(t *testing.T) {
	for _, args := range [][]string{
		nil, {"unknown"}, {"backup"}, {"restore", "--backup-dir", "backup"},
		{"verify", "--backup-dir", "backup", "--data-dir", "data"},
		{"verify", "--backup-dir", "backup", "extra"},
	} {
		var output bytes.Buffer
		if err := run(t.Context(), args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid command emitted success", args, err)
		}
	}
}
