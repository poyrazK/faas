// pure_helpers_mega3_test.go — Coverage Mega-PR #3 cluster F.2:
// fill pkg/rootfs coverage on small pure setters, typed-error
// methods, and t.TempDir-backed FS helpers.
//
// Targets (baseline 74.1% on the package at branch time):
//   - WithSigner (0%): chainable setter on *Builder.
//   - ErrTarballExceedsCap.Error / Is (0%): typed-error methods.
//   - InjectFunctionRunner (0%): t.TempDir-backed happy +
//     missing-path branches.
//
// Whitebox `package rootfs`.

package rootfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBuilderWithSigner_Mega3(t *testing.T) {
	t.Parallel()
	b := NewBuilder(nil)
	got := b.WithSigner(stubSignerMega3{})
	if got != b {
		t.Error("WithSigner: must return receiver")
	}
	if b.signer == nil {
		t.Error("WithSigner(non-nil): signer not wired")
	}
	b.WithSigner(nil)
	if b.signer != nil {
		t.Error("WithSigner(nil): signer not cleared")
	}
}

type stubSignerMega3 struct{}

func (stubSignerMega3) Sign(_ context.Context, _, _ string) error { return nil }

func TestErrTarballExceedsCap_Error_Mega3(t *testing.T) {
	t.Parallel()
	e := &ErrTarballExceedsCap{
		WrittenBytes: 1024,
		EntryBytes:   512,
		CapBytes:     256,
	}
	got := e.Error()
	want := "function tarball exceeds cap: 1024 bytes written, 512 declared in next entry, 256 cap"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrTarballExceedsCap_Is_Mega3(t *testing.T) {
	t.Parallel()
	a := &ErrTarballExceedsCap{WrittenBytes: 1, EntryBytes: 2, CapBytes: 3}
	b := &ErrTarballExceedsCap{WrittenBytes: 9, EntryBytes: 9, CapBytes: 9}
	c := errors.New("unrelated")

	if !a.Is(b) {
		t.Error("Is(same-type): want true")
	}
	if a.Is(c) {
		t.Error("Is(unrelated): want false")
	}
	if !errors.Is(a, &ErrTarballExceedsCap{}) {
		t.Error("errors.Is(a, &ErrTarballExceedsCap{}): want true")
	}
}

func TestInjectFunctionRunner_HappyPath_Mega3(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	src := filepath.Join(t.TempDir(), "faas-runner-src")
	mustWriteFileMega3(t, src, []byte("#!/bin/sh\necho hi\n"))

	if err := InjectFunctionRunner(staging, src); err != nil {
		t.Fatalf("InjectFunctionRunner: %v", err)
	}

	dst := filepath.Join(staging, "usr", "local", "bin", "faas-runner")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "#!/bin/sh\necho hi\n" {
		t.Errorf("dst content = %q, want %q", got, "#!/bin/sh\necho hi\n")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm()&0o755 != 0o755 {
		t.Errorf("dst perms = %o, missing requested 0755 bits (umask-fragile check)", info.Mode().Perm())
	}
}

func TestInjectFunctionRunner_DigestMatchesCopiedBytes(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	src := filepath.Join(t.TempDir(), "faas-runner-src")
	data := []byte("#!/bin/sh\necho digest\n")
	mustWriteFileMega3(t, src, data)

	digest, err := injectFunctionRunner(staging, src)
	if err != nil {
		t.Fatalf("injectFunctionRunner: %v", err)
	}
	wantSum := sha256.Sum256(data)
	want := "sha256:" + hex.EncodeToString(wantSum[:])
	if digest != want {
		t.Fatalf("digest = %q, want %q", digest, want)
	}
	got, err := os.ReadFile(filepath.Join(staging, "usr", "local", "bin", "faas-runner"))
	if err != nil {
		t.Fatalf("read injected runner: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("injected bytes = %q, want %q", got, data)
	}
}

func TestInjectFunctionRunner_MissingSource_Mega3(t *testing.T) {
	t.Parallel()
	err := InjectFunctionRunner(t.TempDir(), filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("InjectFunctionRunner(missing): want error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("InjectFunctionRunner(missing) error chain = %v, want wrapping os.ErrNotExist", err)
	}
}

func mustWriteFileMega3(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
