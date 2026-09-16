package e2etest

// The signer and the verifier must come from ONE keypair.
//
// writeScheddSignPub generated a fresh cosign keypair, wrote only the public
// half for schedd (FAAS_SIGN_PUB), and discarded the private half. imaged then
// signed with the host's /etc/faas/secrets/sign.key — a different keypair — so
// every snapshot prime failed:
//
//	snapshot prime failed: sig_invalid: signature does not match ext4:
//	ECDSA P-256 verification failed for layer
//
// The deployment went to `failed`, so no build test could reach `live`. It
// read as a signing bug; it was a wiring bug.

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteScheddSignPub_WritesAMatchingPrivateHalf(t *testing.T) {
	h := &Harness{SockDir: t.TempDir()}

	pubPath := writeScheddSignPub(t, h)

	if h.SignKeyPath == "" {
		t.Fatal("writeScheddSignPub did not record a private key path; imaged would " +
			"fall back to the host key and every snapshot prime would fail sig_invalid")
	}

	priv := parsePrivate(t, h.SignKeyPath)
	pub := parsePublic(t, pubPath)

	if !priv.PublicKey.Equal(pub) {
		t.Error("the private key imaged signs with does not match the public key " +
			"schedd verifies with; snapshot prime would fail with " +
			"ECDSA P-256 verification failed")
	}
}

// The private key must not be world-readable: it is the platform's attestation
// signer, even in a test harness.
func TestWriteScheddSignPub_PrivateKeyIsNotWorldReadable(t *testing.T) {
	h := &Harness{SockDir: t.TempDir()}
	_ = writeScheddSignPub(t, h)

	info, err := os.Stat(h.SignKeyPath)
	if err != nil {
		t.Fatalf("stat sign.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("sign.key mode %#o is group/world accessible", perm)
	}
}

// Both halves live in the harness's own socket dir, so they are per-test and
// cannot collide between parallel harnesses.
func TestWriteScheddSignPub_KeysLiveInTheHarnessDir(t *testing.T) {
	dir := t.TempDir()
	h := &Harness{SockDir: dir}
	pubPath := writeScheddSignPub(t, h)

	for name, p := range map[string]string{"public": pubPath, "private": h.SignKeyPath} {
		if filepath.Dir(p) != dir {
			t.Errorf("%s key at %q is outside the harness dir %q", name, p, dir)
		}
	}
}

func parsePrivate(t *testing.T, path string) *ecdsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("private key at %s is not PEM", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse PKCS8: %v", err)
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("private key is %T, want *ecdsa.PrivateKey", key)
	}
	return ec
}

func parsePublic(t *testing.T, path string) *ecdsa.PublicKey {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("public key at %s is not PEM", path)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse PKIX: %v", err)
	}
	ec, ok := key.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key is %T, want *ecdsa.PublicKey", key)
	}
	return ec
}
