package e2etest

import (
	"os"
	"path/filepath"
	"testing"
)

// A host with a real builder base must keep it: imaged validates the staged
// ext4's contents and rejects a stub, exiting at boot.
func TestOverrideBuilderBase_KeepsARealBase(t *testing.T) {
	real := filepath.Join(t.TempDir(), "runner-builder.ext4")
	if err := os.WriteFile(real, []byte("not empty"), 0o644); err != nil {
		t.Fatalf("write fake base: %v", err)
	}
	t.Setenv("FAAS_BUILDER_BASE_PATH", real)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", "")

	OverrideBuilderBase(t, "127.0.0.1:5000/onebox-faas/builder-base@sha256:abc")

	if got := os.Getenv("FAAS_TEST_BUILDER_BASE_REF"); got != "" {
		t.Errorf("override applied on a host with a real base at %s: FAAS_TEST_BUILDER_BASE_REF=%q; "+
			"imaged would reject the stub and exit at boot", real, got)
	}
}

// Lima and credential-less CI have no base of their own — that is the case the
// override exists for, and it must still work.
func TestOverrideBuilderBase_AppliesWhenNoBaseIsStaged(t *testing.T) {
	t.Setenv("FAAS_BUILDER_BASE_PATH", filepath.Join(t.TempDir(), "absent.ext4"))
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", "")

	const stub = "127.0.0.1:5000/onebox-faas/builder-base@sha256:abc"
	OverrideBuilderBase(t, stub)

	if got := os.Getenv("FAAS_TEST_BUILDER_BASE_REF"); got != stub {
		t.Errorf("FAAS_TEST_BUILDER_BASE_REF = %q, want the stub %q; a host with no base "+
			"has nothing for imaged to pull", got, stub)
	}
}

// A zero-byte file is a failed or interrupted stage, not a usable base.
func TestOverrideBuilderBase_TreatsAnEmptyBaseAsAbsent(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "runner-builder.ext4")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatalf("write empty base: %v", err)
	}
	t.Setenv("FAAS_BUILDER_BASE_PATH", empty)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", "")

	const stub = "127.0.0.1:5000/onebox-faas/builder-base@sha256:abc"
	OverrideBuilderBase(t, stub)

	if got := os.Getenv("FAAS_TEST_BUILDER_BASE_REF"); got != stub {
		t.Errorf("a zero-byte base was treated as usable; FAAS_TEST_BUILDER_BASE_REF = %q", got)
	}
}

// StagedBuilderBasePath must mirror cmd/imaged's builderBasePathFromEnv, or
// the check consults a path imaged never uses.
func TestStagedBuilderBasePathMirrorsImaged(t *testing.T) {
	t.Setenv("FAAS_BUILDER_BASE_PATH", "")
	t.Setenv("FAAS_STORAGE_ROOT", "/custom/root")
	if got, want := filepath.Dir(StagedBuilderBasePath()), "/custom/root/base"; got != want {
		t.Errorf("base dir = %q, want %q (imaged joins FAAS_STORAGE_ROOT with \"base\")", got, want)
	}

	t.Setenv("FAAS_BUILDER_BASE_PATH", "/explicit/base.ext4")
	if got := StagedBuilderBasePath(); got != "/explicit/base.ext4" {
		t.Errorf("explicit FAAS_BUILDER_BASE_PATH ignored: got %q", got)
	}
}
