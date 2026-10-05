//go:build linux || darwin

package main

// adr: 632

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func TestRuntimeQuotaKeysOperatorProvisioning(t *testing.T) {
	out, readErr, restore := swapIO(t)
	defer restore()
	issuer := uuid.NewString()
	dir := filepath.Join(t.TempDir(), "cell")
	args := []string{"--issuer", issuer, "--role", "cell", "--dir", dir}
	if code := run(append([]string{"runtime-quota-keys", "init"}, args...)); code != 0 {
		t.Fatal("init failed", code, readErr())
	}
	if code := cmdRuntimeQuotaKeys(append([]string{"status"}, args...)); code != 0 {
		t.Fatal("status failed", code, readErr())
	}
	if code := cmdRuntimeQuotaKeys(append([]string{"init"}, args...)); code == 0 {
		t.Fatal("overwrote keys")
	}
	private, err := os.ReadFile(filepath.Join(dir, runtimequota.PrivateKeyFilename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String()+readErr(), "PRIVATE KEY") || strings.Contains(out.String()+readErr(), string(private)) {
		t.Fatal("private key appeared in output")
	}
	if !strings.Contains(out.String(), "issuer="+issuer) || !strings.Contains(out.String(), "key_id=") {
		t.Fatal("missing public status")
	}
	next := filepath.Join(t.TempDir(), "next")
	if _, err := runtimequota.ProvisionAuthorityKeys(next, runtimequota.Cell, issuer); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "overlap.json")
	if code := cmdRuntimeQuotaKeys([]string{"trust", "--input", filepath.Join(dir, runtimequota.TrustFilename), "--input", filepath.Join(next, runtimequota.TrustFilename), "--out", bundle}); code != 0 {
		t.Fatal("overlap bundle failed", code, readErr())
	}
	anchors, err := runtimequota.LoadTrustAnchors(bundle)
	if err != nil || len(anchors) != 2 {
		t.Fatal("missing rotation anchor", err)
	}
	if code := cmdRuntimeQuotaKeys([]string{"trust", "--input", bundle, "--out", bundle}); code == 0 {
		t.Fatal("overwrote public trust")
	}
}

func TestRuntimeQuotaKeysRejectIncompleteOperatorIntent(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{nil, {"rotate"}, {"init"}, {"init", "--issuer", uuid.NewString(), "--role", "cell", "--dir", "relative"}, {"trust"}, {"status", "--unknown"}} {
		if code := cmdRuntimeQuotaKeys(args); code == 0 {
			t.Fatal("accepted incomplete intent", args)
		}
	}
}
