// commands_doctor_builder_base_test.go — focused tests for the doctor's
// builder-base-ext4 check (issue #938 / PR-B / ADR-114).
//
// The check uses package-level hooks (locateBuilderBasePathHook,
// statHook, lookPathHook, runDebugfsHook) so tests can stub the
// filesystem + debugfs probe without root or special tools. Every
// case asserts the exact severity + message-shape contract so a
// regression that flips a warn to an error (or vice versa) is loud.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
)

// withBuilderBaseHooks installs test hooks for the duration of t and
// restores the originals at cleanup. Path/Stat/LookPath/RunDebugfs
// are all stubbed; missing fields fall back to the production hook
// so partial overrides still work.
type builderBaseHooks struct {
	Path       string
	Stat       func(string) (os.FileInfo, error)
	LookPath   func(string) (string, error)
	RunDebugfs func(ctx context.Context, debugfs, ext4, target string) ([]byte, error)
}

func withBuilderBaseHooks(t *testing.T, h builderBaseHooks) {
	t.Helper()
	origPath, origStat, origLook, origRun := locateBuilderBasePathHook, statHook, lookPathHook, runDebugfsHook
	t.Cleanup(func() {
		locateBuilderBasePathHook = origPath
		statHook = origStat
		lookPathHook = origLook
		runDebugfsHook = origRun
	})
	if h.Path != "" {
		locateBuilderBasePathHook = func() string { return h.Path }
	}
	if h.Stat != nil {
		statHook = h.Stat
	}
	if h.LookPath != nil {
		lookPathHook = h.LookPath
	}
	if h.RunDebugfs != nil {
		runDebugfsHook = h.RunDebugfs
	}
}

// TestCheckBuilderBaseExt4_FileMissing: ext4 is not staged yet →
// SeverityWarn. imaged stages on first cold boot; this finding self-
// resolves after imaged has run.
func TestCheckBuilderBaseExt4_FileMissing(t *testing.T) {
	dir := t.TempDir()
	notExt4 := filepath.Join(dir, "does-not-exist.ext4")
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: notExt4,
		Stat: func(p string) (os.FileInfo, error) {
			return nil, &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
		},
	})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityWarn {
		t.Errorf("severity = %q, want %q", findings[0].Severity, doctorSeverityWarn)
	}
	if findings[0].Check != doctorCheckBuilderBaseExt4 {
		t.Errorf("check = %q, want %q", findings[0].Check, doctorCheckBuilderBaseExt4)
	}
	if !strings.Contains(findings[0].Message, "not staged") {
		t.Errorf("message %q does not name 'not staged'", findings[0].Message)
	}
}

// TestCheckBuilderBaseExt4_DebugfsMissing: ext4 present but no debugfs
// on PATH → SeverityWarn. The check degrades rather than failing
// because macOS dev boxes + minimal containers lack e2fsprogs.
func TestCheckBuilderBaseExt4_DebugfsMissing(t *testing.T) {
	dir := t.TempDir()
	ext4 := filepath.Join(dir, "fake.ext4")
	if err := os.WriteFile(ext4, []byte("not a real ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: ext4,
		Stat: os.Stat,
		LookPath: func(string) (string, error) {
			return "", errors.New("exec: \"debugfs\": executable file not found in $PATH")
		},
	})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityWarn {
		t.Errorf("severity = %q, want %q (debugfs-missing should degrade to warn, not error)",
			findings[0].Severity, doctorSeverityWarn)
	}
	if !strings.Contains(findings[0].Message, "debugfs unavailable") {
		t.Errorf("message %q should name 'debugfs unavailable'", findings[0].Message)
	}
}

// TestCheckBuilderBaseExt4_FilePresent: debugfs runs successfully
// AND the output contains an Inode line → SeverityOK. This is the
// happy path on a Lima box or a deployed production control-plane
// node where imaged has staged the real builder-base image.
func TestCheckBuilderBaseExt4_FilePresent(t *testing.T) {
	dir := t.TempDir()
	ext4 := filepath.Join(dir, "fake.ext4")
	if err := os.WriteFile(ext4, []byte("not a real ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: ext4,
		Stat: os.Stat,
		LookPath: func(string) (string, error) {
			return "/usr/sbin/debugfs", nil
		},
		RunDebugfs: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return []byte("Inode: 12345   File mode: 0755"), nil
		},
	})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityOK {
		t.Errorf("severity = %q, want %q", findings[0].Severity, doctorSeverityOK)
	}
	if !strings.Contains(findings[0].Message, "present") {
		t.Errorf("message %q should name 'present'", findings[0].Message)
	}
}

// TestCheckBuilderBaseExt4_ModernDebugfsMode verifies the e2fsprogs 1.47
// spelling used on current Debian hosts ("Mode:" rather than
// "File mode:").
func TestCheckBuilderBaseExt4_ModernDebugfsMode(t *testing.T) {
	dir := t.TempDir()
	ext4 := filepath.Join(dir, "fake.ext4")
	if err := os.WriteFile(ext4, []byte("not a real ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: ext4,
		Stat: os.Stat,
		LookPath: func(string) (string, error) {
			return "/usr/sbin/debugfs", nil
		},
		RunDebugfs: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return []byte("Inode: 12345   Type: regular   Mode: 0755"), nil
		},
	})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != doctorSeverityOK {
		t.Fatalf("findings = %+v, want one ok finding", findings)
	}
}

// TestCheckBuilderBaseExt4_FileAbsent: debugfs runs AND returns an
// error (file does not exist in the ext4) → SeverityError. This is
// the load-bearing case: the alpine placeholder from
// sealed.env.example:25 produces exactly this finding, so the
// operator sees the broken state before running `gregale deploy`.
func TestCheckBuilderBaseExt4_FileAbsent(t *testing.T) {
	dir := t.TempDir()
	ext4 := filepath.Join(dir, "fake.ext4")
	if err := os.WriteFile(ext4, []byte("not a real ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: ext4,
		Stat: os.Stat,
		LookPath: func(string) (string, error) {
			return "/usr/sbin/debugfs", nil
		},
		RunDebugfs: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return []byte("debugfs: error: file not found: usr/local/bin/faas-guest-init"), errors.New("exit status 1")
		},
	})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityError {
		t.Errorf("severity = %q, want %q (file-absent MUST be error, not warn — this is the load-bearing case)",
			findings[0].Severity, doctorSeverityError)
	}
	if !strings.Contains(findings[0].Message, "missing") {
		t.Errorf("message %q should name 'missing'", findings[0].Message)
	}
}

// Production: the read-through cache evicted fsn-3's builder base, and the
// rc.236 roll ran this doctor after stopping imaged (which would have staged
// it again). The warning failed the roll and left every compute daemon
// stopped. When the roll names the ref imaged stages, an empty cache is
// expected; without a ref it stays a warning.
func TestCheckBuilderBaseExt4_EvictedCacheWithStagingRef(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want string
	}{
		{ref: "ghcr.io/gregale/runner-builder@sha256:abc", want: doctorSeverityOK},
		{ref: "", want: doctorSeverityWarn},
	} {
		t.Run(tc.want, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("FAAS_STORAGE_ROOT", root)
			t.Setenv("FAAS_STORAGE_CACHE_DIR", t.TempDir())
			t.Setenv("FAAS_BOX_ROLE", "compute-only")
			t.Setenv("FAAS_BUILDER_BASE_PATH", "")
			t.Setenv("FAAS_BUILDER_BASE_REF", tc.ref)
			withBuilderBaseHooks(t, builderBaseHooks{
				Path: filepath.Join(root, "base", "runner-builder-"+runtime.GOARCH+".ext4"),
				Stat: func(p string) (os.FileInfo, error) {
					return nil, &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
				},
			})
			findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 || findings[0].Severity != tc.want {
				t.Fatalf("findings = %+v, want one %s finding", findings, tc.want)
			}
		})
	}
}

// TestCheckBuilderBaseExt4_PathOverride verifies the FAAS_BUILDER_BASE_PATH
// env var drives locateBuilderBasePathHook — covered implicitly by
// the production wiring, but pinned here so a future refactor that
// drops the env lookup trips the test.
func TestCheckBuilderBaseExt4_PathOverride(t *testing.T) {
	custom := "/tmp/custom/builder-base.ext4"
	t.Setenv("FAAS_BUILDER_BASE_PATH", custom)
	got := locateBuilderBasePathHook()
	if got != custom {
		t.Errorf("locateBuilderBasePathHook = %q, want %q (FAAS_BUILDER_BASE_PATH override)", got, custom)
	}
}

func TestBuilderBaseRequiredUsesControllerDeploymentRole(t *testing.T) {
	root := t.TempDir()
	gitSHA := "0123456789abcdef0123456789abcdef01234567"
	releaseRoot := filepath.Join(root, gitSHA)
	if err := os.MkdirAll(filepath.Join(releaseRoot, "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "systemd", "faas-apid.service"), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployment, err := releasebundle.Build(releaseRoot, gitSHA, gitSHA, "linux/amd64", time.Now())
	if err != nil {
		t.Fatalf("releasebundle.Build: %v", err)
	}
	if err := releasebundle.Write(releaseRoot, deployment); err != nil {
		t.Fatalf("releasebundle.Write: %v", err)
	}
	t.Setenv("FAAS_BOX_ROLE", "")
	orig := builderBaseRequiredHook
	t.Cleanup(func() { builderBaseRequiredHook = orig })
	builderBaseRequiredHook = func(context.Context) bool { return true }

	if got := builderBaseRequired(context.Background(), &doctorDeps{releasesRoot: root, currentGitSHA: gitSHA}); got {
		t.Fatal("control-plane deployment was classified as requiring builder base")
	}

	if err := os.WriteFile(filepath.Join(releaseRoot, "systemd", "faas-imaged.service"), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployment, err = releasebundle.Build(releaseRoot, gitSHA, gitSHA, "linux/amd64", time.Now())
	if err != nil {
		t.Fatalf("releasebundle.Build compute: %v", err)
	}
	if err := releasebundle.Write(releaseRoot, deployment); err != nil {
		t.Fatalf("releasebundle.Write compute: %v", err)
	}
	if got := builderBaseRequired(context.Background(), &doctorDeps{releasesRoot: root, currentGitSHA: gitSHA}); !got {
		t.Fatal("compute deployment was not classified as requiring builder base")
	}
}

// TestCheckBuilderBaseExt4_DebugfsOutputMalformed: review finding #7
// on PR #940. A previous build of the check accepted "Inode" anywhere
// in the output; a future debugfs version that prints "Inode" as part
// of an error banner would trip a false OK finding. The check now
// requires both "Inode:" and "File mode:" — a malformed-but-zero-
// exit output must surface as SeverityError so the operator sees the
// breakage, not a green dot.
func TestCheckBuilderBaseExt4_DebugfsOutputMalformed(t *testing.T) {
	dir := t.TempDir()
	ext4 := filepath.Join(dir, "fake.ext4")
	if err := os.WriteFile(ext4, []byte("not a real ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path: ext4,
		Stat: os.Stat,
		LookPath: func(string) (string, error) {
			return "/usr/sbin/debugfs", nil
		},
		RunDebugfs: func(_ context.Context, _, _, _ string) ([]byte, error) {
			// Successful exit, but missing the File mode field.
			// A naive "contains Inode" check would mark this OK.
			return []byte("Inode: 12345   Links: 1"), nil
		},
	})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatalf("checkBuilderBaseExt4: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	if findings[0].Severity != doctorSeverityError {
		t.Errorf("severity = %q, want %q (malformed-but-zero-exit output MUST be error, not OK)",
			findings[0].Severity, doctorSeverityError)
	}
}

// withCanonicalBuilderBase points the check at the canonical local path
// under a temp storage root, with a temp read-through cache, on a compute
// box, and returns (canonical path, cache copy path).
func withCanonicalBuilderBase(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	t.Setenv("FAAS_STORAGE_ROOT", root)
	t.Setenv("FAAS_STORAGE_CACHE_DIR", cacheDir)
	t.Setenv("FAAS_BUILDER_BASE_PATH", "")
	t.Setenv("FAAS_BOX_ROLE", "compute-only")
	canonical := filepath.Join(root, "base", "runner-builder-"+runtime.GOARCH+".ext4")
	return canonical, storage.CacheFileForKey(cacheDir, sched.BaseKeyForArch("builder", runtime.GOARCH))
}

// With remote storage (FAAS_STORAGE_LOCAL_PREFIXES=none) imaged stages the
// builder base into the read-through cache, not the legacy local path. The
// doctor reported "not staged" on every fresh production-us compute node,
// which failed the join's strict gate on a correctly staged base.
func TestCheckBuilderBaseExt4_VerifiesTheStorageCacheCopy(t *testing.T) {
	canonical, cached := withCanonicalBuilderBase(t)
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	var inspected string
	withBuilderBaseHooks(t, builderBaseHooks{
		Path:     canonical,
		Stat:     os.Stat,
		LookPath: func(string) (string, error) { return "/usr/sbin/debugfs", nil },
		RunDebugfs: func(_ context.Context, _, ext4, _ string) ([]byte, error) {
			inspected = ext4
			return []byte("Inode: 1200   Type: regular    Mode:  0755"), nil
		},
	})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Severity != doctorSeverityOK {
		t.Fatalf("findings = %+v, want one ok finding", findings)
	}
	if inspected != cached {
		t.Fatalf("debugfs inspected %q, want the cache copy %q", inspected, cached)
	}
}

func TestCheckBuilderBaseExt4_NoLocalOrCachedCopyStillWarns(t *testing.T) {
	canonical, _ := withCanonicalBuilderBase(t)
	withBuilderBaseHooks(t, builderBaseHooks{Path: canonical, Stat: os.Stat})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Severity != doctorSeverityWarn || !strings.Contains(findings[0].Message, "not staged") {
		t.Fatalf("findings = %+v, want one 'not staged' warning", findings)
	}
}

// The cache fallback must not weaken the guest-init check itself.
func TestCheckBuilderBaseExt4_CachedCopyWithoutGuestInitIsAnError(t *testing.T) {
	canonical, cached := withCanonicalBuilderBase(t)
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	withBuilderBaseHooks(t, builderBaseHooks{
		Path:     canonical,
		Stat:     os.Stat,
		LookPath: func(string) (string, error) { return "/usr/sbin/debugfs", nil },
		RunDebugfs: func(_ context.Context, _, _, _ string) ([]byte, error) {
			return []byte("/usr/local/bin/faas-guest-init: File not found by ext2_lookup"), errors.New("exit status 1")
		},
	})
	findings, err := checkBuilderBaseExt4(context.Background(), &doctorDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Severity != doctorSeverityError {
		t.Fatalf("findings = %+v, want one error finding", findings)
	}
}
