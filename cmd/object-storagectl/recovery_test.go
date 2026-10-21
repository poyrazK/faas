package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
)

func TestRecoveryCommandsPreserveProofsAndLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	s, err := objectnode.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateBucket("test-bucket"); err != nil {
		t.Fatal(err)
	}
	id := objectrecovery.NewID()
	o, err := s.Put(t.Context(), "test-bucket", "key", strings.NewReader("body"), 4, objectnode.PutOptions{Receipt: id})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = run(t.Context(), []string{"recovery-enable", "--data-dir", root}, &output); err == nil {
		t.Fatal("maintenance opened a serving node")
	}
	_ = s.Close()
	if err = run(t.Context(), []string{"recovery-enable", "--data-dir", root}, &output); !errors.Is(err, objectnode.ErrReplicationOff) {
		t.Fatal(err)
	}
	if _, err = objectnode.EnableReplication(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"recovery-enable", "recovery-status"} {
		output.Reset()
		if err = run(t.Context(), []string{command, "--data-dir", root}, &output); err != nil {
			t.Fatal(command, err)
		}
		var status objectnode.RecoveryRetention
		if json.Unmarshal(output.Bytes(), &status) != nil || status.Version != 1 || status.RetiredBefore.IsZero() {
			t.Fatal("missing durable retention status", output.String())
		}
	}
	file := filepath.Join(t.TempDir(), "settlement.json")
	a := objectrecovery.Settlement{ID: id, Bucket: "test-bucket", Key: "key", Kind: "put", Status: "committed", Size: 4, ETag: o.ETag}
	body, _ := json.Marshal(a)
	if err = os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = run(t.Context(), []string{"recovery-settle", "--data-dir", root, "--settlement-file", file}, &output); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = run(t.Context(), []string{"recovery-collect", "--data-dir", root, "--limit", "1"}, &output); err != nil {
		t.Fatal(err)
	}
	var page objectnode.RecoveryCollection
	if json.Unmarshal(output.Bytes(), &page) != nil || page.ReceiptsRemoved != 0 || page.Protected != 1 || !page.Done {
		t.Fatal("fresh live proof was collected", output.String())
	}
	for _, args := range [][]string{
		{"recovery-enable"},
		{"recovery-collect", "--data-dir", root, "--limit", "0"},
		{"recovery-collect", "--data-dir", root, "--cursor", "../bad"},
		{"recovery-settle", "--data-dir", root},
		{"recovery-status", "--data-dir", root, "extra"},
	} {
		if err = run(t.Context(), args, &output); err == nil {
			t.Fatal("invalid maintenance arguments accepted", args)
		}
	}
}
