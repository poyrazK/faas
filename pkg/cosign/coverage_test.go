// coverage_test.go — fill the remaining pkg/cosign coverage gaps that
// the focused unit files (cosign_test.go, verify_test.go,
// generate_test.go) deliberately don't touch. Targets:
//
//   - TrustListFromDir (verify.go:267) — 0.0% → covered. The
//     per-app trust list is the load-bearing surface for the
//     require_signed deploy-time gate; the existing verify_test.go
//     covers the directory shape (TrustedPublishersFromDir, single
//     app), but the per-app map keyed by app_id was untested.
//   - SigKeyFor (signer.go:76) — 0.0% → covered. Trivial concat
//     helper, but the wire shape ("sigs/" + layerKey + ".sig") is
//     load-bearing — schedd and imaged derive the sig key from
//     this; a typo breaks wake.
//   - ctxCopy.Do (ctxcopy.go:35) — 75% → covered. Pin the
//     short-write path and ctx-during-read cancellation; both are
//     failure modes for streaming a 1-2 GB layer.
//   - loadPrivateKey / loadPublicKey (signer.go:230, keys.go:91)
//     — 63.2% each. Pin the empty-path guard, non-PEM bytes, wrong
//     PEM type, non-ECDSA payload, non-P256 curve branches.
//   - writeKeyFile (generate.go:103) — 70% → covered. Pin the
//     force-rotate path (the os.Remove fallback) and the
//     force=false over-existing-file refusal at the per-file level.
//   - LocalSigner.Sign / LocalVerifier.Verify (signer.go:162,
//     verifier.go:49) — 67.7% / 65.4%. Pin empty-key guards,
//     ctx.Err pre-sign path, hash-error path, write-error path,
//     close-error path, read-error on layer/sig, storage I/O
//     fallthrough on missing sig blob (verifier).
//   - NewLocalSigner / NewLocalVerifier — pin nil-storage path.
//   - VerifyImageSignature — pin nil puller, empty ref, FetchSignature
//     non-missing-error wrapping.
//   - TrustedPublishersFromDir — pin empty-dir path.

package cosign

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// --- SigKeyFor (signer.go:76) ----------------------------------------

func TestSigKeyFor_WireShape(t *testing.T) {
	cases := []struct {
		layerKey, want string
	}{
		{"apps/test/dep.ext4", "sigs/apps/test/dep.ext4.sig"},
		{"regions/eu-fsn/apps/test/dep.ext4", "regions/eu-fsn/sigs/apps/test/dep.ext4.sig"},
		{"base/x86_64/bionic", "sigs/base/x86_64/bionic.sig"},
		{"apps/foo", "sigs/apps/foo.sig"},
	}
	for _, c := range cases {
		if got := SigKeyFor(c.layerKey); got != c.want {
			t.Errorf("SigKeyFor(%q) = %q, want %q", c.layerKey, got, c.want)
		}
	}
}

// --- TrustListFromDir (verify.go:267) --------------------------------

// trustWriteAppPEM writes a per-app trust file at the apid-side
// filename shape: <uuid>--<signer>.pem. Returns the app_id used.
func trustWriteAppPEM(t *testing.T, dir, appID, signer string) {
	t.Helper()
	_, pubPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	name := appID + "--" + signer + ".pem"
	if err := os.WriteFile(filepath.Join(dir, name), pubPEM, 0o444); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestTrustListFromDir_Happy(t *testing.T) {
	dir := t.TempDir()
	appA := "11111111-1111-1111-1111-111111111111"
	appB := "22222222-2222-2222-2222-222222222222"
	trustWriteAppPEM(t, dir, appA, "ci-bot")
	trustWriteAppPEM(t, dir, appA, "manual")
	trustWriteAppPEM(t, dir, appB, "ci-bot")
	// Junk file the loader should skip (missing the -- separator).
	if err := os.WriteFile(filepath.Join(dir, "junk.pem"), []byte("ignored"), 0o444); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	// Bad uuid in filename — should be silently skipped.
	if err := os.WriteFile(filepath.Join(dir, "not-a-uuid--ci.pem"), []byte("ignored"), 0o444); err != nil {
		t.Fatalf("write bad uuid: %v", err)
	}
	// Bad signer (uppercase) — should be silently skipped (DNS-1123
	// label pattern is lower-case only).
	if err := os.WriteFile(filepath.Join(dir, appA+"--BadName.pem"), []byte("ignored"), 0o444); err != nil {
		t.Fatalf("write bad signer: %v", err)
	}

	got, err := TrustListFromDir(dir)
	if err != nil {
		t.Fatalf("TrustListFromDir: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d apps, want 2 (A and B); got=%v", len(got), got)
	}
	// Per-app deterministic alphabetical sort.
	if names := publisherNames(got[appA]); !equalStrings(names, []string{"ci-bot", "manual"}) {
		t.Errorf("app A publishers = %v, want [ci-bot manual]", names)
	}
	if names := publisherNames(got[appB]); !equalStrings(names, []string{"ci-bot"}) {
		t.Errorf("app B publishers = %v, want [ci-bot]", names)
	}
}

func TestTrustListFromDir_Missing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	got, err := TrustListFromDir(missing)
	if err != nil {
		t.Errorf("err = %v, want nil (missing dir is non-fatal)", err)
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

func TestTrustListFromDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := TrustListFromDir(dir)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %v, want empty map", got)
	}
}

func TestTrustListFromDir_EmptyPath(t *testing.T) {
	if _, err := TrustListFromDir(""); err == nil {
		t.Error("empty dir: want error")
	}
}

func TestTrustListFromDir_BadPermsPropagate(t *testing.T) {
	// A .pem with bad perms must surface the loader's
	// ErrInsecurePubKeyPerms (loadPublicKey error chain is
	// observed by TrustListFromDir — it does NOT swallow).
	dir := t.TempDir()
	appID := "33333333-3333-3333-3333-333333333333"
	_, pubPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	bad := filepath.Join(dir, appID+"--ci-bot.pem")
	if err := os.WriteFile(bad, pubPEM, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = TrustListFromDir(dir)
	if !errors.Is(err, ErrInsecurePubKeyPerms) {
		t.Errorf("err = %v, want ErrInsecurePubKeyPerms", err)
	}
}

func TestTrustListFromDir_ReadDirOtherError(t *testing.T) {
	// ReadDir on a path that IS a file (not a directory) → not
	// ErrNotExist → wrapped error returned. Pins the "non-missing
	// path error" branch at verify.go:271-277.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "iam-a-file")
	if err := os.WriteFile(filePath, []byte("hello"), 0o444); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := TrustListFromDir(filePath)
	if err == nil {
		t.Fatal("err = nil, want error (path is a file, not a dir)")
	}
	if !strings.Contains(err.Error(), "read trusted-publishers dir") {
		t.Errorf("err = %v, want 'read trusted-publishers dir' prefix", err)
	}
}

// --- ctxCopy.Do (ctxcopy.go:35) --------------------------------------

type shortWriter struct {
	// limit is the max bytes accepted per Write call. After the
	// limit, writes return 0 + ErrShortWrite.
	limit int
	// dropped counts bytes the writer refused to write.
	dropped int
}

func (s *shortWriter) Write(p []byte) (int, error) {
	if len(p) <= s.limit {
		return len(p), nil
	}
	s.dropped += len(p) - s.limit
	n := s.limit
	s.limit = 0
	return n, io.ErrShortWrite
}

func TestCtxCopy_ShortWrite(t *testing.T) {
	src := bytes.NewReader([]byte("0123456789"))
	// First Write accepts up to 5 bytes; subsequent writes return 0 + ErrShortWrite.
	sw := &shortWriter{limit: 5}
	cc := newCtxCopy(sw, src, context.Background())
	n, err := cc.Do()
	if !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("err = %v, want io.ErrShortWrite", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5 (only 5 bytes written before short-write)", n)
	}
}

func TestCtxCopy_ContextCancelledMidRead(t *testing.T) {
	// A reader that observes ctx cancellation: returns an error
	// when ctx is cancelled. ctxCopy.Do checks ctx.Err at the top
	// of every iteration, so cancel() before the second Read
	// call is observed via the next iteration's ctx.Err check.
	ctx, cancel := context.WithCancel(context.Background())
	r := &ctxAwareReader{ctx: ctx}
	cc := newCtxCopy(io.Discard, r, ctx)
	// First Read returns 5 bytes; then we cancel; the next loop
	// iteration must see ctx.Err() and return.
	go func() { cancel() }()
	_, err := cc.Do()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

type ctxAwareReader struct {
	ctx context.Context
}

func (r *ctxAwareReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	for i := range p {
		p[i] = 'x'
		if err := r.ctx.Err(); err != nil {
			return i + 1, err
		}
	}
	return len(p), nil
}

func TestCtxCopy_NormalEOF(t *testing.T) {
	src := bytes.NewReader([]byte("hello"))
	cc := newCtxCopy(io.Discard, src, context.Background())
	n, err := cc.Do()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
}

func TestCtxCopy_ReadError(t *testing.T) {
	src := &errReader{err: errors.New("disk on fire")}
	cc := newCtxCopy(io.Discard, src, context.Background())
	_, err := cc.Do()
	if err == nil || err.Error() != "disk on fire" {
		t.Errorf("err = %v, want 'disk on fire'", err)
	}
}

type errReader struct{ err error }

func (e *errReader) Read(p []byte) (int, error) { return 0, e.err }

// --- loadPrivateKey / loadPublicKey error branches -------------------

func TestLoadPrivateKey_EmptyPath(t *testing.T) {
	if _, err := loadPrivateKey(""); err == nil {
		t.Error("empty path: want error")
	}
}

func TestLoadPrivateKey_NotPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(path, []byte("not a pem block"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadPrivateKey(path); err == nil {
		t.Error("not-PEM file: want error")
	} else if !strings.Contains(err.Error(), "not PEM-encoded") {
		t.Errorf("err = %v, want 'not PEM-encoded'", err)
	}
}

func TestLoadPrivateKey_WrongPEMType(t *testing.T) {
	// Encode the bytes as a PUBLIC KEY block — wrong type for
	// loadPrivateKey (which wants PRIVATE KEY).
	dir := t.TempDir()
	_, pubPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	path := filepath.Join(dir, "sign.key")
	if err := os.WriteFile(path, pubPEM, 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = loadPrivateKey(path)
	if err == nil {
		t.Fatal("wrong PEM type: want error")
	}
	if !strings.Contains(err.Error(), "PEM type PRIVATE KEY") {
		t.Errorf("err = %v, want 'PEM type PRIVATE KEY' fragment", err)
	}
}

func TestLoadPrivateKey_NonECDSAPayload(t *testing.T) {
	// Encode an RSA key as PKCS#8 (so x509.ParsePKCS8PrivateKey
	// succeeds) but in PRIVATE KEY PEM form. The code path that
	// rejects this is the type assertion at signer.go:253 — the
	// parsed key is *rsa.PrivateKey, not *ecdsa.PrivateKey.
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaPriv)
	if err != nil {
		t.Fatalf("MarshalPKCS8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER})

	dir := t.TempDir()
	path := filepath.Join(dir, "sign.key")
	if err := os.WriteFile(path, pemBytes, 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = loadPrivateKey(path)
	if err == nil {
		t.Fatal("RSA PKCS#8 ECDSA-load: want error")
	}
	if !strings.Contains(err.Error(), "not ECDSA") {
		t.Errorf("err = %v, want 'not ECDSA' fragment", err)
	}
}

func TestLoadPrivateKey_NonP256Curve(t *testing.T) {
	// Generate a P-384 key and try to load it as a P-256 key.
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey P-384: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	dir := t.TempDir()
	path := filepath.Join(dir, "sign.key")
	if err := os.WriteFile(path, pemBytes, 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = loadPrivateKey(path)
	if err == nil {
		t.Fatal("P-384: want error (loadPrivateKey requires P-256)")
	}
	if !strings.Contains(err.Error(), "P-256") {
		t.Errorf("err = %v, want 'P-256' fragment", err)
	}
}

func TestLoadPublicKey_EmptyPath(t *testing.T) {
	if _, err := loadPublicKey(""); err == nil {
		t.Error("empty path: want error")
	}
}

func TestLoadPublicKey_NotPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(path, []byte("not a pem block"), 0o444); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadPublicKey(path); err == nil {
		t.Error("not-PEM file: want error")
	}
}

func TestLoadPublicKey_WrongPEMType(t *testing.T) {
	// Encode a PRIVATE KEY block (wrong type for loadPublicKey).
	dir := t.TempDir()
	privPEM, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	path := filepath.Join(dir, "sign-pub.pem")
	if err := os.WriteFile(path, privPEM, 0o444); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = loadPublicKey(path)
	if err == nil {
		t.Fatal("wrong PEM type: want error")
	}
	if !strings.Contains(err.Error(), "PEM type PUBLIC KEY") {
		t.Errorf("err = %v, want 'PEM type PUBLIC KEY' fragment", err)
	}
}

func TestLoadPublicKey_NonECDSAPayload(t *testing.T) {
	// Encode an RSA public key (PKIX SPKI) under a PUBLIC KEY PEM
	// block. The code path that rejects this is the type
	// assertion at keys.go:110 — parsed key is *rsa.PublicKey.
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	rsaDER, err := x509.MarshalPKIXPublicKey(&rsaPriv.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: rsaDER})

	dir := t.TempDir()
	path := filepath.Join(dir, "sign-pub.pem")
	if err := os.WriteFile(path, pemBytes, 0o444); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = loadPublicKey(path)
	if err == nil {
		t.Fatal("RSA pub: want error")
	}
	if !strings.Contains(err.Error(), "not ECDSA") {
		t.Errorf("err = %v, want 'not ECDSA' fragment", err)
	}
}

func TestLoadPublicKey_NonP256Curve(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey P-384: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	dir := t.TempDir()
	path := filepath.Join(dir, "sign-pub.pem")
	if err := os.WriteFile(path, pemBytes, 0o444); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = loadPublicKey(path)
	if err == nil {
		t.Fatal("P-384 pub: want error")
	}
	if !strings.Contains(err.Error(), "P-256") {
		t.Errorf("err = %v, want 'P-256' fragment", err)
	}
}

// --- writeKeyFile (generate.go:103) ----------------------------------

func TestWriteKeyFile_ForceOverwriteRotates(t *testing.T) {
	// The force=true branch at generate.go:117-119 drops the
	// existing file first because the canonical 0440/0400 modes
	// have no owner-w bit; an O_WRONLY open on the existing path
	// would return EACCES. Pin that flow.
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("original"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writeKeyFile(path, []byte("rotated"), 0o400, true); err != nil {
		t.Fatalf("force rotate: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "rotated" {
		t.Errorf("content = %q, want 'rotated'", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o400 {
		t.Errorf("mode = %#o, want 0o400", got)
	}
}

func TestWriteKeyFile_ForceRemovesMissing(t *testing.T) {
	// force=true on a path that doesn't exist → os.Remove returns
	// ErrNotExist, which is swallowed; WriteFile then creates the
	// file. Pin that the swallow is correct (we don't want a
	// confusing "remove failed" error from a first-time force write).
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh")
	if err := writeKeyFile(path, []byte("hello"), 0o400, true); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want 'hello'", got)
	}
}

func TestWriteKeyFile_RefusesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("original"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := writeKeyFile(path, []byte("new"), 0o400, false); err == nil {
		t.Error("want error on existing file (force=false)")
	}
}

// --- LocalSigner.Sign (signer.go:162) error branches -----------------

func TestLocalSigner_NewLocalSigner_NilStorage(t *testing.T) {
	privPath, _ := keyPairTempDir(t)
	if _, err := NewLocalSigner(privPath, nil, nil); err == nil {
		t.Error("nil storage: want error")
	} else if !strings.Contains(err.Error(), "nil storage backend") {
		t.Errorf("err = %v, want 'nil storage backend' fragment", err)
	}
}

func TestLocalSigner_NewLocalSigner_BadKeyPath(t *testing.T) {
	stor := newMemStorage()
	if _, err := NewLocalSigner("/no/such/key", stor, nil); err == nil {
		t.Error("missing key path: want error")
	}
}

func TestLocalSigner_Sign_EmptyKeys(t *testing.T) {
	privPath, _ := keyPairTempDir(t)
	signer, err := NewLocalSigner(privPath, newMemStorage(), nil)
	if err != nil {
		t.Fatalf("NewLocalSigner: %v", err)
	}
	if err := signer.Sign(context.Background(), "", "sig"); err == nil {
		t.Error("empty layerKey: want error")
	}
	if err := signer.Sign(context.Background(), "layer", ""); err == nil {
		t.Error("empty sigKey: want error")
	}
}

func TestLocalSigner_Sign_PreSignCtxCancelled(t *testing.T) {
	privPath, _ := keyPairTempDir(t)
	signer, err := NewLocalSigner(privPath, newMemStorage(), nil)
	if err != nil {
		t.Fatalf("NewLocalSigner: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := signer.Sign(ctx, "layer", "sig"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestLocalSigner_Sign_ReadFailure(t *testing.T) {
	privPath, _ := keyPairTempDir(t)
	signer, err := NewLocalSigner(privPath, newMemStorage(), nil)
	if err != nil {
		t.Fatalf("NewLocalSigner: %v", err)
	}
	// No blob at "missing-layer" → stor.Get returns ErrNotFound
	// → Sign must surface the wrapped read error.
	err = signer.Sign(context.Background(), "missing-layer", "sig")
	if err == nil {
		t.Fatal("missing layer: want error")
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("err = %v, want storage.ErrNotFound in chain", err)
	}
}

func TestLocalSigner_Sign_WriteFailure(t *testing.T) {
	// Storage backend whose Put returns an error. Pin the put-error
	// branch at signer.go:222.
	privPath, _ := keyPairTempDir(t)
	base := newMemStorage()
	if err := base.Put(context.Background(), "layer", bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("seed layer: %v", err)
	}
	stor := newWrapStorage(base)
	stor.putDeniedError = fmt.Errorf("write denied")

	signer, err := NewLocalSigner(privPath, stor, nil)
	if err != nil {
		t.Fatalf("NewLocalSigner: %v", err)
	}
	if err := signer.Sign(context.Background(), "layer", "sigs/x"); err == nil {
		t.Error("write failure: want error")
	} else if !strings.Contains(err.Error(), "write sig") {
		t.Errorf("err = %v, want 'write sig' fragment", err)
	}
}

func TestLocalSigner_Sign_CloseErrLogged(t *testing.T) {
	// Layer reader's Close returns an error — Sign should log via
	// the optional logger and continue. Pin that the close-error
	// path doesn't break the Sign contract.
	privPath, _ := keyPairTempDir(t)
	base := newMemStorage()
	if err := base.Put(context.Background(), "layer", bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stor := newWrapStorage(base)
	// Replace the layer reader with a close-erroring one.
	stor.getOverrides = map[string]io.ReadCloser{
		"layer": &closeErringReadCloser{Reader: bytes.NewReader([]byte("data"))},
	}

	spy := &spyLogger{}
	signer, err := NewLocalSigner(privPath, stor, spy)
	if err != nil {
		t.Fatalf("NewLocalSigner: %v", err)
	}
	if err := signer.Sign(context.Background(), "layer", "sig"); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if spy.warns == 0 {
		t.Error("close-error Warn call not observed")
	}
}

type closeErringReadCloser struct{ io.Reader }

func (c *closeErringReadCloser) Close() error { return fmt.Errorf("close boom") }

type spyLogger struct{ warns int }

func (s *spyLogger) Warn(_ string, _ ...any) { s.warns++ }

// --- LocalVerifier (verifier.go) error branches ----------------------

func TestLocalVerifier_NewLocalVerifier_NilStorage(t *testing.T) {
	_, pubPath := keyPairTempDir(t)
	if _, err := NewLocalVerifier(pubPath, nil); err == nil {
		t.Error("nil storage: want error")
	}
}

func TestLocalVerifier_Verify_EmptyKeys(t *testing.T) {
	_, pubPath := keyPairTempDir(t)
	v, err := NewLocalVerifier(pubPath, newMemStorage())
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}
	if err := v.Verify(context.Background(), "", "sig"); err == nil {
		t.Error("empty layerKey: want error")
	}
	if err := v.Verify(context.Background(), "layer", ""); err == nil {
		t.Error("empty sigKey: want error")
	}
}

func TestLocalVerifier_Verify_LayerReadFailure(t *testing.T) {
	_, pubPath := keyPairTempDir(t)
	v, err := NewLocalVerifier(pubPath, newMemStorage())
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}
	if err := v.Verify(context.Background(), "no-layer", "no-sig"); err == nil {
		t.Error("missing layer: want error")
	} else if !strings.Contains(err.Error(), "read layer") {
		t.Errorf("err = %v, want 'read layer' fragment", err)
	}
}

func TestLocalVerifier_Verify_LayerCloseError(t *testing.T) {
	// Close() on the layer reader returns an error. Verify must
	// surface the close error wrapped via "cosign: close layer".
	_, pubPath := keyPairTempDir(t)
	base := newMemStorage()
	if err := base.Put(context.Background(), "layer", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := base.Put(context.Background(), "sig", bytes.NewReader(bytes.Repeat([]byte{0}, 64))); err != nil {
		t.Fatalf("seed sig: %v", err)
	}
	stor := newWrapStorage(base)
	stor.getOverrides = map[string]io.ReadCloser{
		"layer": &closeErringReadCloser{Reader: bytes.NewReader([]byte("payload"))},
	}

	v, err := NewLocalVerifier(pubPath, stor)
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}
	if err := v.Verify(context.Background(), "layer", "sig"); err == nil {
		t.Error("close error: want error")
	} else if !strings.Contains(err.Error(), "close layer") {
		t.Errorf("err = %v, want 'close layer' fragment", err)
	}
}

func TestLocalVerifier_Verify_SigReadFailure(t *testing.T) {
	// Layer exists, sig blob missing → storage.IsNotFound → the
	// "signature missing" Problem path. Already covered by
	// cosign_test.go::TestLocalVerifier_MissingSig. The OTHER
	// branch is non-NotFound read error → wrapped sig-read error.
	_, pubPath := keyPairTempDir(t)
	base := newMemStorage()
	if err := base.Put(context.Background(), "layer", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stor := newWrapStorage(base)
	// sig blob returns a non-NotFound error.
	stor.getErrors = map[string]error{"sig": fmt.Errorf("disk on fire")}
	v, err := NewLocalVerifier(pubPath, stor)
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}
	err = v.Verify(context.Background(), "layer", "sig")
	if err == nil {
		t.Fatal("sig read error: want error")
	}
	if !strings.Contains(err.Error(), "read sig") {
		t.Errorf("err = %v, want 'read sig' fragment", err)
	}
}

func TestLocalVerifier_Verify_SigReadAllError(t *testing.T) {
	// Layer exists; sig reader returns a non-EOF, non-Nil error
	// during io.ReadAll. Pin the io.ReadAll error path at
	// verifier.go:85-89.
	_, pubPath := keyPairTempDir(t)
	base := newMemStorage()
	if err := base.Put(context.Background(), "layer", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stor := newWrapStorage(base)
	stor.getOverrides = map[string]io.ReadCloser{
		"sig": &errReadCloser{err: fmt.Errorf("read partial")},
	}

	v, err := NewLocalVerifier(pubPath, stor)
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}
	err = v.Verify(context.Background(), "layer", "sig")
	if err == nil {
		t.Fatal("sig read all error: want error")
	}
	if !strings.Contains(err.Error(), "read sig") {
		t.Errorf("err = %v, want 'read sig' fragment", err)
	}
}

func TestLocalVerifier_Verify_SigCloseError(t *testing.T) {
	_, pubPath := keyPairTempDir(t)
	base := newMemStorage()
	if err := base.Put(context.Background(), "layer", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := base.Put(context.Background(), "sig", bytes.NewReader(bytes.Repeat([]byte{0}, 64))); err != nil {
		t.Fatalf("seed sig: %v", err)
	}
	sigBytes := bytes.Repeat([]byte{0}, 64)
	stor := newWrapStorage(base)
	stor.getOverrides = map[string]io.ReadCloser{
		"sig": &closeErringReadCloser{Reader: bytes.NewReader(sigBytes)},
	}

	v, err := NewLocalVerifier(pubPath, stor)
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}
	if err := v.Verify(context.Background(), "layer", "sig"); err == nil {
		t.Error("sig close error: want error")
	} else if !strings.Contains(err.Error(), "close sig") {
		t.Errorf("err = %v, want 'close sig' fragment", err)
	}
}

type errReadCloser struct{ err error }

func (e *errReadCloser) Read(p []byte) (int, error) { return 0, e.err }
func (e *errReadCloser) Close() error               { return nil }

// --- memStorage extensions for layer-override tests ------------------

// wrapStorage layers per-key Get/Put overrides on top of a base
// memStorage. Used by the close-error and partial-read tests that
// need to surface a specific io.ReadCloser for a single key
// without disturbing other keys. Defined here to avoid conflicting
// with the Get method declared in cosign_test.go.
type wrapStorage struct {
	*memStorage
	getOverrides   map[string]io.ReadCloser
	getErrors      map[string]error
	putDeniedError error
}

func newWrapStorage(base *memStorage) *wrapStorage {
	return &wrapStorage{memStorage: base}
}

func (w *wrapStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if rc, ok := w.getOverrides[key]; ok {
		return rc, nil
	}
	if err, ok := w.getErrors[key]; ok {
		return nil, err
	}
	//nolint:contextcheck // override seam: caller-supplied ctx is intentionally bypassed so the underlying memStorage read isn't tied to a test-only timeout.
	return w.memStorage.Get(context.Background(), key)
}

func (w *wrapStorage) Put(ctx context.Context, key string, r io.Reader) error {
	if w.putDeniedError != nil {
		return w.putDeniedError
	}
	return w.memStorage.Put(ctx, key, r)
}

// --- VerifyImageSignature (verify.go:165) error branches ------------

func TestVerifyImageSignature_NilPuller(t *testing.T) {
	_, _, err := VerifyImageSignature(context.Background(), nil, "ref", nil)
	if err == nil {
		t.Fatal("nil puller: want error")
	}
	if !strings.Contains(err.Error(), "nil ImageSignaturePuller") {
		t.Errorf("err = %v, want nil puller fragment", err)
	}
}

func TestVerifyImageSignature_EmptyRef(t *testing.T) {
	_, _, err := VerifyImageSignature(context.Background(), &fakePuller{}, "", nil)
	if err == nil {
		t.Fatal("empty ref: want error")
	}
	if !strings.Contains(err.Error(), "empty image ref") {
		t.Errorf("err = %v, want empty ref fragment", err)
	}
}

func TestVerifyImageSignature_ResolveDigestError(t *testing.T) {
	puller := &fakePuller{} // unknown ref → "fakePuller: unknown ref" error
	_, _, err := VerifyImageSignature(context.Background(), puller, "ref", []TrustedPublisher{{Name: "x", PublicKey: &ecdsa.PublicKey{}}})
	if err == nil {
		t.Fatal("resolve digest error: want error")
	}
	if !strings.Contains(err.Error(), "resolve digest") {
		t.Errorf("err = %v, want 'resolve digest' fragment", err)
	}
}

func TestVerifyImageSignature_FetchSignatureOtherError(t *testing.T) {
	// FetchSignature returns a non-ErrSignatureMissing error →
	// wrapped "fetch signature" error.
	priv, _ := ecdsaP256KeyPair(t)
	digest := sha256OfBytes([]byte("manifest"))
	ref := "registry.example.com/app@sha256:" + hexDigest(digest[:])
	puller := &fakePuller{
		digestFor: map[string]string{ref: "sha256:" + hexDigest(digest[:])},
		sigFor:    map[string][]byte{ref: signDigestOverWire(t, priv, digest)},
	}
	// Inject a non-missing fetch error: re-route the puller via a
	// wrapper that returns "network down".
	wrapper := &fetchErrPuller{
		inner: puller,
		err:   fmt.Errorf("network down"),
	}
	_, _, err := VerifyImageSignature(context.Background(), wrapper, ref, []TrustedPublisher{{Name: "x", PublicKey: &priv.PublicKey}})
	if err == nil {
		t.Fatal("fetch signature error: want error")
	}
	if !strings.Contains(err.Error(), "fetch signature") {
		t.Errorf("err = %v, want 'fetch signature' fragment", err)
	}
	if errors.Is(err, ErrSignatureMissing) {
		t.Error("non-missing fetch error should not be classified as ErrSignatureMissing")
	}
}

type fetchErrPuller struct {
	inner ImageSignaturePuller
	err   error
}

func (f *fetchErrPuller) ResolveDigest(ctx context.Context, ref string) (string, error) {
	return f.inner.ResolveDigest(ctx, ref)
}

func (f *fetchErrPuller) FetchSignature(_ context.Context, _, _ string) ([]byte, error) {
	return nil, f.err
}

func TestVerifyImageSignature_AllPublishersHaveNilPub(t *testing.T) {
	// Every TrustedPublisher has PublicKey=nil → walk falls
	// through without verifying → ErrSignatureInvalid (no match).
	priv, _ := ecdsaP256KeyPair(t)
	digest := sha256OfBytes([]byte("manifest"))
	ref := "registry.example.com/app@sha256:" + hexDigest(digest[:])
	puller := &fakePuller{
		digestFor: map[string]string{ref: "sha256:" + hexDigest(digest[:])},
		sigFor:    map[string][]byte{ref: signDigestOverWire(t, priv, digest)},
	}
	pubs := []TrustedPublisher{
		{Name: "x", PublicKey: nil},
		{Name: "y", PublicKey: nil},
	}
	_, _, err := VerifyImageSignature(context.Background(), puller, ref, pubs)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestVerifyImageSignature_MalformedDigest(t *testing.T) {
	// ResolveDigest returns a non-sha256 digest → digestBytesFromDigest
	// fails → wrapped "parse digest" error.
	puller := &fakePuller{
		digestFor: map[string]string{"ref": "not-a-digest"},
		sigFor:    map[string][]byte{"ref": make([]byte, 64)},
	}
	priv, _ := ecdsaP256KeyPair(t)
	_, _, err := VerifyImageSignature(context.Background(), puller, "ref", []TrustedPublisher{{Name: "x", PublicKey: &priv.PublicKey}})
	if err == nil {
		t.Fatal("malformed digest: want error")
	}
	if !strings.Contains(err.Error(), "parse digest") {
		t.Errorf("err = %v, want 'parse digest' fragment", err)
	}
}

// --- hexByte (verify.go:239) direct coverage -------------------------

func TestHexByte_DigitMap(t *testing.T) {
	cases := []struct {
		c    byte
		want byte
		ok   bool
	}{
		{'0', 0, true},
		{'9', 9, true},
		{'a', 10, true},
		{'f', 15, true},
		{'g', 0, false},
		{'A', 0, false}, // upper-case not accepted; canonical sha256 is lower-case
		{'/', 0, false},
		{':', 0, false},
	}
	for _, c := range cases {
		got, ok := hexByte(c.c)
		if ok != c.ok || got != c.want {
			t.Errorf("hexByte(%q) = (%d,%v), want (%d,%v)", c.c, got, ok, c.want, c.ok)
		}
	}
}

// --- TrustedPublishersFromDir (verify.go:98) edge cases --------------

func TestTrustedPublishersFromDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := TrustedPublishersFromDir(dir)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %v, want empty slice", got)
	}
}

func TestTrustedPublishersFromDir_EmptyPath(t *testing.T) {
	if _, err := TrustedPublishersFromDir(""); err == nil {
		t.Error("empty path: want error")
	}
}

func TestTrustedPublishersFromDir_ReadDirOtherError(t *testing.T) {
	// Path is a file, not a directory → not ErrNotExist → wrapped
	// error returned. Pins the non-missing path branch.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o444); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := TrustedPublishersFromDir(filePath)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "read trusted-publishers dir") {
		t.Errorf("err = %v, want 'read trusted-publishers dir' prefix", err)
	}
}

func TestTrustedPublishersFromDir_BadPubKeyFile(t *testing.T) {
	// A .pem that fails LoadPublicKeyFile for a non-perm reason
	// (malformed PEM bytes) must surface the wrapped error from
	// the loader.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ci-bot.pem"), []byte("not pem"), 0o444); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := TrustedPublishersFromDir(dir)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "load trusted publisher") {
		t.Errorf("err = %v, want 'load trusted publisher' fragment", err)
	}
}

// --- helpers ---------------------------------------------------------

func publisherNames(ps []TrustedPublisher) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
