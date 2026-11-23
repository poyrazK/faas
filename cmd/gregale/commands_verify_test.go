package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/verifycontract"
)

func TestVerifyValidateSummarizesChecks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, verifycontract.FileName)
	if err := os.WriteFile(file, []byte("version: 1\nchecks:\n  - {name: health, http: {path: /healthz}}\n  - {name: order, advisory: true, http: {method: POST, path: /orders, json: {a: 1}}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()
	if code := cmdVerify([]string{"validate", "--file", file}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"2 checks", "health", "required", "advisory, previews and branches only"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if err := os.WriteFile(file, []byte("version: 1\nchecks: [{name: x, http: {path: nope}}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdVerify([]string{"validate", "--file", file}); code == 0 {
		t.Fatal("invalid contract validated")
	}
}

func TestRenderVerificationContractReport(t *testing.T) {
	report := &verifycontract.Report{Status: verifycontract.StatusFailed, Environment: verifycontract.EnvironmentProduction, Checks: []verifycontract.CheckResult{
		{Name: "products", Kind: "http", Required: true, Status: "failed", HTTPStatus: 500, Failures: []string{"status 500 is not 2xx"}},
		{Name: "order", Kind: "http", Status: "skipped"},
	}}
	var out bytes.Buffer
	renderVerificationContractReport(&out, report)
	for _, want := range []string{"verify:", "failed (production", "products", "HTTP 500", "status 500 is not 2xx", "order", "advisory"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
	out.Reset()
	renderVerificationContractReport(&out, nil)
	if out.Len() != 0 {
		t.Fatal("nil report printed output")
	}
}
