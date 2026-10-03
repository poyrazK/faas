//go:build linux

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestProcessCredentialUsesCompanionImageGroup(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "etc"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "passwd"), []byte("server:x:1001:2001::/:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "group"), []byte("readers:x:3001:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	credential, err := processCredential(root, "server:readers")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Uid != 1001 || credential.Gid != 3001 {
		t.Fatalf("credential=%+v", credential)
	}
	if uid := lookupUIDInRoot(root, "server:readers"); uid != 1001 {
		t.Fatalf("UID-only consumer got %d", uid)
	}
	if _, err := processCredential(root, "server:missing"); err == nil {
		t.Fatal("missing explicit group accepted")
	}
}

func TestExecHealthcheckUsesDistinctGroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("credential transition requires root")
	}
	id, err := exec.LookPath("id")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := processCredential("", "1001:2001")
	if err != nil {
		t.Fatal(err)
	}
	report := execHealthcheckWithOptions(context.Background(), []string{id, "-g"}, time.Second, 1001, nil, "", &syscall.SysProcAttr{Credential: credential}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if report.Status != healthcheckStatusPass || strings.TrimSpace(string(report.Output)) != "2001" {
		t.Fatalf("report=%+v", report)
	}
}

func TestAppTaskCommandUsesDistinctGroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("credential transition requires root")
	}
	id, err := exec.LookPath("id")
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	result, err := executeAppTaskCommand(context.Background(), apptaskproto.Request{Command: []string{id, "-g"}}, api.AppManifest{User: "1001:2001"}, nil, nil, &output, io.Discard)
	if err != nil || result.Status != apptaskproto.StatusSucceeded || strings.TrimSpace(output.String()) != "2001" {
		t.Fatalf("result=%+v output=%q error=%v", result, output.String(), err)
	}
}
