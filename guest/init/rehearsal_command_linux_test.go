//go:build linux

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

func TestRehearsalCommandExecutesPinnedFilesAndReturnsPrivateArtifacts(t *testing.T) {
	dir := t.TempDir()
	spec := rehearsalcommand.Spec{Version: 1, RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", Command: []string{"/bin/sh", "-c", `cat fixtures/input; printf '%s' "$VALUE"; printf '{"version":1,"observations":[]}' > "$GREGALE_TEST_BEHAVIOR_OUTPUT"; printf '{"id":"task-1"}' > "$GREGALE_TEST_TRIGGER_OUTPUT"`}, Env: map[string]string{"VALUE": "$(false);literal"}, Files: map[string][]byte{"fixtures/input": []byte("trusted-input|")}}
	argv, err := rehearsalcommand.Encode(spec)
	if err != nil {
		t.Fatal(err)
	}
	req := apptaskproto.Request{Version: apptaskproto.RehearsalVersion, TaskID: "22222222-2222-2222-2222-222222222222", Command: argv, TimeoutSeconds: 2, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes}
	var output strings.Builder
	result, err := executeAppTaskCommand(t.Context(), req, api.AppManifest{User: "0", WorkingDir: dir}, nil, nil, &output, io.Discard)
	if err != nil || result.Status != apptaskproto.StatusSucceeded {
		t.Fatalf("command failed: %+v %v", result, err)
	}
	r, err := rehearsalcommand.ParseReceipt([]byte(output.String()), req.TaskID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Stdout) != "trusted-input|$(false);literal" || len(r.Behavior) == 0 || len(r.Trigger) == 0 {
		t.Fatalf("lost command artifacts: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "behavior.json")); !os.IsNotExist(err) {
		t.Fatal("private output leaked into source directory")
	}
}

func TestRehearsalCommandRefusesSymlinkInputsAndArtifactDevices(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "inputs")); err != nil {
		t.Fatal(err)
	}
	if err := installRehearsalInputs(dir, map[string][]byte{"inputs/data": []byte("unsafe")}, nil); err == nil {
		t.Fatal("input symlink traversed")
	}
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "behavior.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := readRehearsalArtifact(dir, "behavior.json", 128); err == nil {
		t.Fatal("artifact symlink followed")
	}
	if err := os.WriteFile(filepath.Join(dir, "large"), make([]byte, 129), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRehearsalArtifact(dir, "large", 128); err == nil {
		t.Fatal("oversized artifact accepted")
	}
}

func TestRehearsalCommandCancellationHasNoCompletionReceipt(t *testing.T) {
	spec := rehearsalcommand.Spec{Version: 1, RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", Command: []string{"/bin/sh", "-c", "sleep 30"}}
	argv, _ := rehearsalcommand.Encode(spec)
	req := apptaskproto.Request{Version: apptaskproto.RehearsalVersion, TaskID: "22222222-2222-2222-2222-222222222222", Command: argv, TimeoutSeconds: 1, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	var output strings.Builder
	_, err := executeAppTaskCommand(ctx, req, api.AppManifest{User: "0", WorkingDir: t.TempDir()}, nil, nil, &output, io.Discard)
	if err == nil || output.Len() != 0 {
		t.Fatalf("cancellation produced completion: %v %s", err, output.String())
	}
}
