package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// adr: 1141
func TestWorkloadIdentityInitRotateStatus(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wi")
	var out, errOut bytes.Buffer
	if code := cmdWorkloadIdentityWrite(subInit, []string{"--out", dir}, &out, &errOut); code != 0 {
		t.Fatalf("init exit %d: %s", code, errOut.String())
	}
	for name, mode := range map[string]os.FileMode{
		workloadIdentityKeyFile: 0o400, workloadIdentityKIDFile: 0o444, workloadIdentityJWKSFile: 0o444,
	} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: mode %v err %v, want %v", name, info.Mode().Perm(), err, mode)
		}
	}
	first := readKID(t, dir)

	// The key, its kid file and the JWKS fit together: a token vmmd mints
	// with them verifies against the JWKS apid loads.
	assertMintVerifies(t, dir, first)

	if code := cmdWorkloadIdentityWrite(subInit, []string{"--out", dir}, &out, &errOut); code == 0 {
		t.Fatal("init overwrote an existing key")
	}
	out.Reset()
	if code := cmdWorkloadIdentityWrite(subRotate, []string{"--out", dir}, &out, &errOut); code != 0 {
		t.Fatalf("rotate exit %d: %s", code, errOut.String())
	}
	second := readKID(t, dir)
	if second == first || !strings.Contains(out.String(), first) {
		t.Fatalf("rotate: kid %s -> %s, output %q", first, second, out.String())
	}
	assertMintVerifies(t, dir, second)

	out.Reset()
	if code := cmdWorkloadIdentityStatus([]string{"--out", dir}, &out, &errOut); code != 0 {
		t.Fatalf("status exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "jwks key ids: "+second+", "+first) {
		t.Fatalf("status output %q", out.String())
	}
}

func TestWorkloadIdentityRotateNeedsAKey(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := cmdWorkloadIdentityWrite(subRotate, []string{"--out", t.TempDir()}, &out, &errOut); code == 0 {
		t.Fatal("rotate without a key succeeded")
	}
	if code := cmdWorkloadIdentityWrite(subInit, nil, &out, &errOut); code == 0 {
		t.Fatal("init without --out succeeded")
	}
}

func readKID(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, workloadIdentityKIDFile))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func assertMintVerifies(t *testing.T, dir, kid string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, workloadIdentityKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	key, err := workloadidentity.ParseRSAPrivateKeyPEM(raw)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, kid, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	token, err := signer.Mint(now, "acct", "app", "inst", workloadidentity.QueuesAudience)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workloadidentity.LoadVerifier(filepath.Join(dir, workloadIdentityJWKSFile), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(token.AccessToken, workloadidentity.QueuesAudience, now); err != nil {
		t.Fatalf("token signed with kid %s does not verify: %v", kid, err)
	}
}
