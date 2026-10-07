// adr: 380
package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFOCUSCLIExportsAndPreservesExistingFiles(t *testing.T) {
	data := []byte{'P', 'K', 0, 255, '\n'}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/focus" || r.URL.Query().Get("month") != "2026-09" || r.URL.Query().Get("format") != "zip" || r.Header.Get("Authorization") != "Bearer fp_live_x" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	path := filepath.Join(t.TempDir(), "billing.zip")
	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdBilling([]string{"export", "--month", "2026-09", "--out", path}); code != 0 {
		t.Fatalf("export exit=%d stderr=%s", code, stderr.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("file=%v error=%v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || !strings.Contains(stderr.String(), "partial") {
		t.Fatalf("file permissions or conformance notice: %v %s", err, stderr.String())
	}
	if code := cmdBillingExport([]string{"--month", "2026-09", "--out", path}); code == 0 {
		t.Fatalf("overwrote existing file, exit=%d", code)
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("existing file changed")
	}
}

func TestFOCUSCLIValidationAndAPIFailureLeaveNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Export unavailable", "invalid invoice"))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	path := filepath.Join(t.TempDir(), "billing.zip")
	for _, args := range [][]string{
		{}, {"--month", "2026-09"}, {"--month", "2026-13", "--out", path},
		{"--month", "2026-09", "--format", "parquet", "--out", path},
		{"--month", "2026-09", "--out", path, "unexpected"},
		{"--month", "2026-09", "--out", path},
	} {
		if code := cmdBillingExport(args); code == 0 {
			t.Errorf("unexpected success: %v", args)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("failed export created a file: %v", err)
		}
	}
}

func TestFOCUSCLICSVStdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "csv" {
			t.Errorf("format=%s", r.URL.Query().Get("format"))
		}
		_, _ = w.Write([]byte("BilledCost,BillingAccountId\n1.00,account-1\n"))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdBillingExport([]string{"--month", "2026-09", "--format", "csv"}); code != 0 || stdout.String() != "BilledCost,BillingAccountId\n1.00,account-1\n" {
		t.Fatalf("stdout=%q exit=%d", stdout.String(), code)
	}
}
