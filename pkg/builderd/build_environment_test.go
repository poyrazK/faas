package builderd

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReadBuildEnvironmentFromHost(t *testing.T) {
	base := os.Getenv("FAAS_TEST_BUILDER_BASE")
	if base == "" {
		t.Skip("FAAS_TEST_BUILDER_BASE is not set")
	}
	environment, err := readBuildEnvironment(base, "", runtime.GOOS+"/"+runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if !validSHA256Digest(environment.BuilderBaseIdentity) {
		t.Fatalf("invalid builder base identity %q", environment.BuilderBaseIdentity)
	}
	if environment.TargetPlatform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("target platform=%q", environment.TargetPlatform)
	}
}

func TestCurrentBuildEnvironmentRejectsIncompleteIdentity(t *testing.T) {
	for name, vm := range map[string]*fakeVM{
		"provider error": {environmentErr: errors.New("unavailable")},
		"empty identity": {environment: BuildEnvironment{TargetPlatform: "linux/amd64"}},
		"empty platform": {environment: BuildEnvironment{BuilderBaseIdentity: "builder-a"}},
		"blank identity": {environment: BuildEnvironment{BuilderBaseIdentity: " ", TargetPlatform: "linux/amd64"}},
		"blank platform": {environment: BuildEnvironment{BuilderBaseIdentity: "builder-a", TargetPlatform: "\t"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := currentBuildEnvironment(vm); err == nil {
				t.Fatal("incomplete build environment was accepted")
			}
		})
	}

	got, err := currentBuildEnvironment(&fakeVM{environment: BuildEnvironment{
		BuilderBaseIdentity: " builder-a ", TargetPlatform: " linux/amd64 ",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got.BuilderBaseIdentity != "builder-a" || got.TargetPlatform != "linux/amd64" {
		t.Fatalf("environment was not normalized: %+v", got)
	}
}

func TestReadBuildEnvironment(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "runner-builder-amd64.ext4")
	if err := os.WriteFile(base, []byte("ext4"), 0o640); err != nil {
		t.Fatal(err)
	}
	sidecar := "sha256:" + strings.Repeat("a", sha256.Size*2) + "\nfaas-base-layout-v3\nguest-init-sha256=" + strings.Repeat("b", sha256.Size*2)
	if err := os.WriteFile(base+".digest", []byte(sidecar+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	got, err := readBuildEnvironment(base, "", "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := builderIdentity("sha256:"+strings.Repeat("a", sha256.Size*2), "faas-base-layout-v3")
	wantBaseDigest := "sha256:" + strings.Repeat("a", sha256.Size*2)
	if got.BuilderBaseIdentity != wantIdentity || got.BaseDigest != wantBaseDigest || got.TargetPlatform != "linux/amd64" {
		t.Fatalf("environment=%+v want identity=%q base=%q platform=linux/amd64", got, wantIdentity, wantBaseDigest)
	}
}

func TestReadBuildEnvironmentRejectsUntrustedSidecar(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, base string)
	}{
		{name: "missing base", setup: func(*testing.T, string) {}},
		{name: "empty base", setup: func(t *testing.T, base string) {
			if err := os.WriteFile(base, nil, 0o640); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing sidecar", setup: func(t *testing.T, base string) {
			if err := os.WriteFile(base, []byte("ext4"), 0o640); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "malformed sidecar", setup: func(t *testing.T, base string) {
			if err := os.WriteFile(base, []byte("ext4"), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(base+".digest", []byte("not-a-digest\nlayout"), 0o640); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "sidecar predates base", setup: func(t *testing.T, base string) {
			if err := os.WriteFile(base, []byte("ext4"), 0o640); err != nil {
				t.Fatal(err)
			}
			sidecarPath := base + ".digest"
			body := "sha256:" + strings.Repeat("c", sha256.Size*2) + "\nfaas-base-layout-v3"
			if err := os.WriteFile(sidecarPath, []byte(body), 0o640); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if err := os.Chtimes(sidecarPath, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(base, now, now); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "runner-builder-amd64.ext4")
			tc.setup(t, base)
			if _, err := readBuildEnvironment(base, "", "linux/amd64"); err == nil {
				t.Fatal("unsafe builder identity was accepted")
			}
		})
	}
}

// A release that only rebuilds guest-init must keep the build cache warm; a
// new builder image or base layout must not.
func TestReadBuildEnvironmentIdentityIgnoresGuestInitBinary(t *testing.T) {
	config := "sha256:" + strings.Repeat("a", sha256.Size*2)
	identity := func(t *testing.T, sidecar string) string {
		t.Helper()
		base := filepath.Join(t.TempDir(), "runner-builder-amd64.ext4")
		if err := os.WriteFile(base, []byte("ext4"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".digest", []byte(sidecar), 0o640); err != nil {
			t.Fatal(err)
		}
		env, err := readBuildEnvironment(base, "", "linux/amd64")
		if err != nil {
			t.Fatal(err)
		}
		return env.BuilderBaseIdentity
	}
	release1 := identity(t, config+"\nfaas-base-layout-v3\nguest-init-sha256="+strings.Repeat("b", 64)+"\nsource-ref=ghcr.io/x/builder-base@"+config)
	for name, sidecar := range map[string]string{
		"new guest-init": config + "\nfaas-base-layout-v3\nguest-init-sha256=" + strings.Repeat("c", 64) + "\nsource-ref=ghcr.io/x/builder-base@" + config,
		"legacy sidecar": config + "\nfaas-base-layout-v3",
		"no source ref":  config + "\nfaas-base-layout-v3\nguest-init-sha256=" + strings.Repeat("d", 64),
	} {
		if got := identity(t, sidecar); got != release1 {
			t.Errorf("%s: identity %s, want %s (cache must survive a guest-init-only release)", name, got, release1)
		}
	}
	for name, sidecar := range map[string]string{
		"new builder image": "sha256:" + strings.Repeat("e", 64) + "\nfaas-base-layout-v3",
		"new base layout":   config + "\nfaas-base-layout-v4",
	} {
		if got := identity(t, sidecar); got == release1 {
			t.Errorf("%s: identity unchanged, want a cold cache", name)
		}
	}
}
