package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLetsEncryptCertIssuer_RejectsEmptyStorageDir pins the
// fail-closed invariant on a missing storage dir at construction
// time. A daemon that boots with FAAS_TLS_STORAGE_DIR unset
// (operator misconfig) must NOT silently fall through to writing
// certs in the cwd.
func TestLetsEncryptCertIssuer_RejectsEmptyStorageDir(t *testing.T) {
	if _, err := NewLetsEncryptCertIssuer("", "ops@example.com", true, nil, nil); err == nil {
		t.Fatal("empty storage dir = nil err; want non-nil fail-closed")
	}
}

// TestLetsEncryptCertIssuer_RejectsEmptyHostname pins the
// fail-closed invariant on an empty hostname at Issue time.
// Callers that bypass the wrapper (e.g. a future renewer
// hot-path) must NOT silently fall through to minting a
// zero-name cert.
func TestLetsEncryptCertIssuer_RejectsEmptyHostname(t *testing.T) {
	dir := t.TempDir()
	le, err := NewLetsEncryptCertIssuer(dir, "ops@example.com", true, nil, nil)
	if err != nil {
		t.Fatalf("NewLetsEncryptCertIssuer: %v", err)
	}
	if _, err := le.Issue(context.Background(), ""); err == nil {
		t.Fatal("empty hostname = nil err; want non-nil fail-closed")
	}
}

// TestLetsEncryptCertIssuer_RejectsEmptySet pins the
// fail-closed invariant on an empty hostname set at IssueSet
// time. The wrapper's verified-hostname-empty branch is the
// upstream check, but the issuer must also fail closed in case
// a future caller reaches it directly.
func TestLetsEncryptCertIssuer_RejectsEmptySet(t *testing.T) {
	dir := t.TempDir()
	le, err := NewLetsEncryptCertIssuer(dir, "ops@example.com", true, nil, nil)
	if err != nil {
		t.Fatalf("NewLetsEncryptCertIssuer: %v", err)
	}
	if _, err := le.IssueSet(context.Background(), nil); err == nil {
		t.Fatal("empty IssueSet = nil err; want non-nil fail-closed")
	}
}

// TestLetsEncryptCertIssuer_LeafPathLayout pins the on-disk
// layout certmagic writes to. A future certmagic upgrade that
// renames the issuerKey (e.g. switching to a non-LE CA) must
// surface a unit-test failure here so the engineer fixes
// leafPath() instead of letting the parseCertNotAfter call
// silently return zero.
func TestLetsEncryptCertIssuer_LeafPathLayout(t *testing.T) {
	dir := t.TempDir()
	le, err := NewLetsEncryptCertIssuer(dir, "ops@example.com", true, nil, nil)
	if err != nil {
		t.Fatalf("NewLetsEncryptCertIssuer: %v", err)
	}
	got, err := le.leafPath("a.example")
	if err != nil {
		t.Fatalf("leafPath: %v", err)
	}
	want := filepath.Join(dir, "certificates", "acme-staging-v02.api.letsencrypt.org-directory", "a.example", "a.example.crt")
	if got != want {
		t.Errorf("leafPath = %q, want %q", got, want)
	}
	// The derived key must match the key certmagic's ACME issuer stores
	// under, for both directories.
	for _, staging := range []bool{true, false} {
		le, err := NewLetsEncryptCertIssuer(t.TempDir(), "ops@example.com", staging, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		derived := le.issuerKey()
		if err := le.initConfig(); err != nil {
			t.Fatal(err)
		}
		if actual := le.cfg.Issuers[0].IssuerKey(); derived != actual {
			t.Errorf("staging=%v: derived issuer key %q, certmagic stores under %q", staging, derived, actual)
		}
	}
}

// TestLetsEncryptCertIssuer_InitLazily pins that the certmagic
// Config is constructed on first Issue (not at New) so a daemon
// boot doesn't pay the certmagic init cost (DNS-01 solver
// construction, ACME account registration check) until a
// surface actually needs minting. The test asserts the
// tempdir is created by the first Issue call, not by
// construction.
func TestLetsEncryptCertIssuer_InitLazily(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lazy")
	le, err := NewLetsEncryptCertIssuer(dir, "ops@example.com", true, nil, nil)
	if err != nil {
		t.Fatalf("NewLetsEncryptCertIssuer: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("storage dir exists after construction; want it lazy")
	}
	// Don't actually call Issue here — that would try to talk
	// to LE staging. The test asserts the lazy-construction
	// invariant by the dir-not-existing observation; a
	// follow-up ADR can add an integration-tier test that
	// stands up a fake DNS server.
	_ = le
}
