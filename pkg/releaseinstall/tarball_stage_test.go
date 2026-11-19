package releaseinstall_test

// adr: 20261119090000

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

// TestStagedTarball_InstallRoundTrip: one staging pass yields the manifest,
// verifies like Verify, publishes the same bin tree as Extract, and leaves
// nothing in the releases root once closed.
func TestStagedTarball_InstallRoundTrip(t *testing.T) {
	tb, identity := signedTarball(t)
	root := t.TempDir()
	staged, err := releaseinstall.StageTarball(tb.Packed, root)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if len(staged.Manifest()) == 0 {
		t.Fatal("staging did not keep the manifest member")
	}
	got, err := tb.VerifyStaged(context.Background(), &releaseinstall.FixtureCosignVerifier{Identity: identity}, staged)
	if err != nil || got != identity {
		t.Fatalf("verify staged = %q, %v; want %q", got, err, identity)
	}
	if err := tb.PublishStaged(root, staged); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := releaseinstall.Verify(root, tb.Manifest); err != nil {
		t.Fatalf("on-disk verify after publish: %v", err)
	}
	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != tb.GitSHA {
			t.Fatalf("releases root keeps %q after close; want only the release", e.Name())
		}
	}
}

// TestStagedTarball_RejectsTampered: a corrupted archive fails before anything
// reaches bin.
func TestStagedTarball_RejectsTampered(t *testing.T) {
	tb, identity := signedTarball(t)
	tampered := append([]byte(nil), tb.Packed...)
	tampered[len(tampered)/2] ^= 0x01
	tb2 := *tb
	tb2.Packed = tampered
	root := t.TempDir()
	staged, err := releaseinstall.StageTarball(tb2.Packed, root)
	if err == nil {
		defer func() { _ = staged.Close() }()
		_, err = tb2.VerifyStaged(context.Background(), &releaseinstall.FixtureCosignVerifier{Identity: identity}, staged)
	}
	if !errors.Is(err, releaseinstall.ErrTarballTampered) {
		t.Fatalf("tampered tarball: got %v, want ErrTarballTampered", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, tb.GitSHA)); !os.IsNotExist(statErr) {
		t.Fatalf("a tampered tarball published a release directory (stat err %v)", statErr)
	}
}

// TestStagedTarball_RequiresSignature: a staged tarball without a cosign
// bundle fails closed, as Verify does.
func TestStagedTarball_RequiresSignature(t *testing.T) {
	tb, identity := signedTarball(t)
	tb.Sig = nil
	staged, err := releaseinstall.StageTarball(tb.Packed, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = staged.Close() }()
	if _, err := tb.VerifyStaged(context.Background(), &releaseinstall.FixtureCosignVerifier{Identity: identity}, staged); !errors.Is(err, releaseinstall.ErrCosignSigMissing) {
		t.Fatalf("unsigned staged tarball: got %v, want ErrCosignSigMissing", err)
	}
}
