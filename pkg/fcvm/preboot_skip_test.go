// adr: 192
// spec: §6.3
package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func v2CaptureKey(id string) string {
	return state.SnapshotCaptureMemKey("dep-1", state.SnapshotTierInit, id)
}

// A restore of a capture whose drive already holds identical pre-boot files
// skips the loop mount; any other restore writes exactly as before.
func TestStagePreBootFilesUnless_SkipsOnlyKnownIdenticalCaptures(t *testing.T) {
	fakeLoopMounts(t)
	v := newStagingVMM(t, "cold")
	secrets, env, resolver := []byte(`{"S":"x"}`), []byte(`{"K":"v"}`), "10.100.0.1"

	// Cold boot writes and records what the instance's drive holds.
	if err := v.stagePreBootFiles("cold", nil, secrets, env, resolver, false); err != nil {
		t.Fatal(err)
	}
	capture := v2CaptureKey("c1")
	v.preBoot.captured("cold", capture)

	for _, tc := range []struct {
		name       string
		captureKey string
		secrets    []byte
		env        []byte
		resolver   string
		wantSkip   bool
	}{
		{name: "same capture, same inputs", captureKey: capture, secrets: secrets, env: env, resolver: resolver, wantSkip: true},
		{name: "rotated secret", captureKey: capture, secrets: []byte(`{"S":"y"}`), env: env, resolver: resolver},
		{name: "changed env", captureKey: capture, secrets: secrets, env: []byte(`{"K":"w"}`), resolver: resolver},
		{name: "different resolver", captureKey: capture, secrets: secrets, env: env, resolver: "10.100.0.2"},
		{name: "capture this vmmd never saw", captureKey: v2CaptureKey("other"), secrets: secrets, env: env, resolver: resolver},
		{name: "cold boot path", captureKey: "", secrets: secrets, env: env, resolver: resolver},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions, _ := fakeLoopMounts(t) // a fresh drive per restore
			inst := "restore-" + tc.name
			v2 := newStagingVMMFrom(t, v, inst)
			before := *sessions
			skipped, err := v2.stagePreBootFilesUnless(inst, tc.captureKey, nil, tc.secrets, tc.env, tc.resolver, false)
			if err != nil {
				t.Fatal(err)
			}
			if skipped != tc.wantSkip {
				t.Fatalf("skipped = %v, want %v", skipped, tc.wantSkip)
			}
			if mounted := *sessions - before; mounted != map[bool]int{true: 0, false: 1}[tc.wantSkip] {
				t.Fatalf("loop mount sessions = %d (skipped=%v)", mounted, skipped)
			}
		})
	}
}

// newStagingVMMFrom gives instance a chroot with drive1 while sharing v's
// ledger, the way one vmmd restores many instances.
func newStagingVMMFrom(t *testing.T, v *JailerVMM, instance string) *JailerVMM {
	t.Helper()
	fresh := newStagingVMM(t, instance)
	fresh.preBoot = v.preBoot
	return fresh
}

// A skipped restore passes its digest on, so a capture taken from the
// restored instance is itself skippable on the next restore.
func TestPreBootLedger_ChainsThroughSkippedRestores(t *testing.T) {
	sessions, _ := fakeLoopMounts(t)
	v := newStagingVMM(t, "a")
	if err := v.stagePreBootFiles("a", nil, nil, nil, "10.100.0.1", false); err != nil {
		t.Fatal(err)
	}
	v.preBoot.captured("a", v2CaptureKey("c1"))
	b := newStagingVMMFrom(t, v, "b")
	if skipped, err := b.stagePreBootFilesUnless("b", v2CaptureKey("c1"), nil, nil, nil, "10.100.0.1", false); err != nil || !skipped {
		t.Fatalf("first restore skipped=%v err=%v", skipped, err)
	}
	b.preBoot.captured("b", v2CaptureKey("c2"))
	c := newStagingVMMFrom(t, v, "c")
	before := *sessions
	if skipped, err := c.stagePreBootFilesUnless("c", v2CaptureKey("c2"), nil, nil, nil, "10.100.0.1", false); err != nil || !skipped {
		t.Fatalf("restore of a capture from a skipped restore: skipped=%v err=%v", skipped, err)
	}
	if *sessions != before {
		t.Fatal("chained restore still mounted drive1")
	}
}

// Legacy captures carry no drive, so a restore stages the pristine app layer
// and must always write; an unknown instance records nothing.
func TestPreBootLedger_OnlyV2CapturesOfKnownInstances(t *testing.T) {
	p := newPreBootLedger()
	p.instanceHas("i", "digest")
	p.captured("i", "snap/dep-1/mem")
	if p.captureHas("snap/dep-1/mem", "digest") {
		t.Fatal("legacy capture key recorded")
	}
	p.captured("unknown", v2CaptureKey("c1"))
	if p.captureHas(v2CaptureKey("c1"), "digest") {
		t.Fatal("capture of an instance with no recorded writes recorded")
	}
	p.captured("i", v2CaptureKey("c1"))
	if !p.captureHas(v2CaptureKey("c1"), "digest") {
		t.Fatal("v2 capture of a known instance not recorded")
	}
	p.forget("i")
	p.captured("i", v2CaptureKey("c2"))
	if p.captureHas(v2CaptureKey("c2"), "digest") {
		t.Fatal("forgotten instance still recorded")
	}
	var nilLedger *preBootLedger
	nilLedger.captured("i", v2CaptureKey("c3"))
	if nilLedger.captureHas(v2CaptureKey("c3"), "digest") {
		t.Fatal("nil ledger reported a capture")
	}
}

// The capture map is bounded and evicts the oldest entry first.
func TestPreBootLedger_Bounded(t *testing.T) {
	p := newPreBootLedger()
	p.instanceHas("i", "d")
	for n := 0; n < preBootLedgerMaxCaptures+3; n++ {
		p.captured("i", v2CaptureKey(fmt.Sprintf("c%d", n)))
	}
	if len(p.captures) != preBootLedgerMaxCaptures {
		t.Fatalf("captures = %d, want %d", len(p.captures), preBootLedgerMaxCaptures)
	}
	if p.captureHas(v2CaptureKey("c0"), "d") {
		t.Fatal("oldest capture not evicted")
	}
}

// Every input feeds the digest, so no changed payload can be skipped.
func TestPreBootFileWriters_DigestCoversEveryInput(t *testing.T) {
	wl := func(env string) []WorkloadSpec {
		return []WorkloadSpec{
			{Name: "main", Type: "app", RamMB: 256, Port: 8080, Essential: true},
			{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9100, preparedEnvJSON: []byte(env)},
		}
	}
	digest := func(w []WorkloadSpec, s, e []byte, ip string, task bool) string {
		t.Helper()
		_, d, err := preBootFileWriters(w, s, e, ip, task)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := digest(wl(`{"A":"1"}`), []byte("s"), []byte("e"), "10.100.0.1", false)
	if again := digest(wl(`{"A":"1"}`), []byte("s"), []byte("e"), "10.100.0.1", false); again != base {
		t.Fatal("digest is not deterministic")
	}
	for name, d := range map[string]string{
		"secrets":             digest(wl(`{"A":"1"}`), []byte("t"), []byte("e"), "10.100.0.1", false),
		"env":                 digest(wl(`{"A":"1"}`), []byte("s"), []byte("f"), "10.100.0.1", false),
		"resolver":            digest(wl(`{"A":"1"}`), []byte("s"), []byte("e"), "10.100.0.2", false),
		"app task":            digest(wl(`{"A":"1"}`), []byte("s"), []byte("e"), "10.100.0.1", true),
		"sidecar env":         digest(wl(`{"A":"2"}`), []byte("s"), []byte("e"), "10.100.0.1", false),
		"secret/env boundary": digest(wl(`{"A":"1"}`), []byte("se"), []byte(""), "10.100.0.1", false),
	} {
		if d == base {
			t.Errorf("changing %s did not change the digest", name)
		}
	}
}

// preBootInputs is one restore's pre-boot payload.
type preBootInputs struct {
	secrets, env []byte
	resolver     string
}

var learnInputs = preBootInputs{secrets: []byte(`{"S":"x"}`), env: []byte(`{"K":"v"}`), resolver: "10.100.0.1"}

func (in preBootInputs) writers(t *testing.T) ([]preBootFileWriter, string) {
	t.Helper()
	w, d, err := preBootFileWriters(nil, in.secrets, in.env, in.resolver, false)
	if err != nil {
		t.Fatal(err)
	}
	return w, d
}

// After a vmmd restart the first restore of a capture mounts, finds the files
// already there, writes nothing and records the capture; the next restore of
// it skips the mount. Parks reuse snapshots, so without this the skip never
// fired in production.
func TestStagePreBootFilesUnless_LearnsCaptureAfterRestart(t *testing.T) {
	sessions, mountRoot := fakeLoopMounts(t)
	v := newStagingVMM(t, "first")
	// An earlier vmmd wrote the files onto the drive that was then captured.
	if err := v.stagePreBootFiles("first", nil, learnInputs.secrets, learnInputs.env, learnInputs.resolver, false); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1_000_000_000, 0)
	for _, rel := range []string{secretsEnvPath, apiEnvPath, serviceDiscoveryResolverPath} {
		must(t, os.Chtimes(filepath.Join(*mountRoot, rel), old, old))
	}
	v.preBoot = newPreBootLedger() // this vmmd restarted and knows nothing
	key := v2CaptureKey("reused")
	_, digest := learnInputs.writers(t)

	before := *sessions
	skipped, err := v.stagePreBootFilesUnless("first", key, nil, learnInputs.secrets, learnInputs.env, learnInputs.resolver, false)
	if err != nil || skipped || *sessions-before != 1 {
		t.Fatalf("first restore after restart: skipped=%v err=%v mounts=%d, want one mount", skipped, err, *sessions-before)
	}
	for _, rel := range []string{secretsEnvPath, apiEnvPath, serviceDiscoveryResolverPath} {
		if info, err := os.Lstat(filepath.Join(*mountRoot, rel)); err != nil || !info.ModTime().Equal(old) {
			t.Fatalf("%s already on the drive was rewritten (%v)", rel, err)
		}
	}
	if !v.preBoot.captureHas(key, digest) {
		t.Fatal("capture not learned from its staged drive")
	}

	next := newStagingVMMFrom(t, v, "second")
	before = *sessions
	skipped, err = next.stagePreBootFilesUnless("second", key, nil, learnInputs.secrets, learnInputs.env, learnInputs.resolver, false)
	if err != nil || !skipped || *sessions != before {
		t.Fatalf("second restore: skipped=%v err=%v mounts=%d, want a skip without a mount", skipped, err, *sessions-before)
	}

	// The record is keyed on the inputs too: a rotated secret still mounts
	// and writes (onto a fresh drive; the test user cannot rewrite 0400
	// files the way root vmmd does).
	sessions, _ = fakeLoopMounts(t)
	rotated := newStagingVMMFrom(t, v, "rotated")
	if skipped, err := rotated.stagePreBootFilesUnless("rotated", key, nil, []byte(`{"S":"y"}`), learnInputs.env, learnInputs.resolver, false); err != nil || skipped || *sessions != 1 {
		t.Fatalf("rotated secret: skipped=%v err=%v mounts=%d, want one mount that writes", skipped, err, *sessions)
	}
}

// A drive missing the files is written as before and never recorded.
func TestStagePreBootFilesUnless_DoesNotLearnFromADriveWithoutTheFiles(t *testing.T) {
	sessions, mountRoot := fakeLoopMounts(t)
	v := newStagingVMM(t, "empty")
	key := v2CaptureKey("empty")
	skipped, err := v.stagePreBootFilesUnless("empty", key, nil, learnInputs.secrets, learnInputs.env, learnInputs.resolver, false)
	if err != nil || skipped || *sessions != 1 {
		t.Fatalf("skipped=%v err=%v mounts=%d, want one mount that writes", skipped, err, *sessions)
	}
	if _, err := os.Stat(filepath.Join(*mountRoot, apiEnvPath)); err != nil {
		t.Fatalf("env.json not written: %v", err)
	}
	if _, digest := learnInputs.writers(t); v.preBoot.captureHas(key, digest) {
		t.Fatal("capture recorded from a drive that did not hold the files")
	}
}

// Cold boot and legacy restores keep their unconditional writes: they never
// inspect the drive, even when it happens to hold identical files.
func TestStagePreBootFilesUnless_NonV2RestoresAlwaysWrite(t *testing.T) {
	for _, key := range []string{"", "snap/dep-1/mem"} {
		t.Run("key="+key, func(t *testing.T) {
			_, mountRoot := fakeLoopMounts(t)
			v := newStagingVMM(t, "i")
			if err := v.stagePreBootFiles("i", nil, learnInputs.secrets, learnInputs.env, learnInputs.resolver, false); err != nil {
				t.Fatal(err)
			}
			old := time.Unix(1_000_000_000, 0)
			env := filepath.Join(*mountRoot, apiEnvPath)
			must(t, os.Chtimes(env, old, old))
			_, err := v.stagePreBootFilesUnless("i", key, nil, learnInputs.secrets, learnInputs.env, learnInputs.resolver, false)
			// Root vmmd rewrites the 0400 files; an unprivileged test user is
			// refused. Either way the write was attempted.
			if err == nil {
				if info, statErr := os.Stat(env); statErr != nil || info.ModTime().Equal(old) {
					t.Fatalf("drive inspected instead of written (%v)", statErr)
				}
			} else if !errors.Is(err, os.ErrPermission) {
				t.Fatal(err)
			}
		})
	}
}

// Only a v2 capture stages a captured drive; nothing else is ever learned.
func TestPreBootLedger_LearnsOnlyV2Captures(t *testing.T) {
	p := newPreBootLedger()
	for _, key := range []string{"", "snap/dep-1/mem"} {
		if p.learnable(key) {
			t.Errorf("learnable(%q) = true", key)
		}
		p.learned(key, "d")
		if p.captureHas(key, "d") {
			t.Errorf("learned %q", key)
		}
	}
	p.learned(v2CaptureKey("c1"), "")
	if p.captureHas(v2CaptureKey("c1"), "") {
		t.Error("learned an empty digest")
	}
	p.learned(v2CaptureKey("c1"), "d")
	if !p.captureHas(v2CaptureKey("c1"), "d") {
		t.Error("v2 capture not learned")
	}
	var nilLedger *preBootLedger
	if nilLedger.learnable(v2CaptureKey("c1")) {
		t.Error("nil ledger is learnable")
	}
	nilLedger.learned(v2CaptureKey("c1"), "d")
}

// Presence means exactly what the write would leave. The drive is
// tenant-writable, so links are never followed — not even ones that stay
// inside the mount — and nothing outside the mount is ever read.
func TestPreBootFilesPresent_OnlyExactFiles(t *testing.T) {
	outside := t.TempDir()
	replace := func(t *testing.T, path string, blob []byte, mode os.FileMode) {
		must(t, os.Remove(path))
		must(t, os.WriteFile(path, blob, mode))
	}
	for _, tc := range []struct {
		name   string
		want   bool
		tamper func(t *testing.T, mountRoot string)
	}{
		{name: "exactly as written", want: true},
		{name: "tenant edited a file", tamper: func(t *testing.T, mr string) {
			replace(t, filepath.Join(mr, apiEnvPath), []byte(`{"K":"tenant"}`), 0o400)
		}},
		{name: "same length, different bytes", tamper: func(t *testing.T, mr string) {
			replace(t, filepath.Join(mr, apiEnvPath), []byte(`{"K":"w"}`), 0o400)
		}},
		{name: "file missing", tamper: func(t *testing.T, mr string) {
			must(t, os.Remove(filepath.Join(mr, secretsEnvPath)))
		}},
		{name: "loosened mode", tamper: func(t *testing.T, mr string) {
			must(t, os.Chmod(filepath.Join(mr, secretsEnvPath), 0o644))
		}},
		{name: "resolver not world-readable", tamper: func(t *testing.T, mr string) {
			must(t, os.Chmod(filepath.Join(mr, serviceDiscoveryResolverPath), 0o600))
		}},
		{name: "file is a directory", tamper: func(t *testing.T, mr string) {
			p := filepath.Join(mr, secretsEnvPath)
			must(t, os.Remove(p))
			must(t, os.Mkdir(p, 0o400))
		}},
		{name: "file is a link to an identical file", tamper: func(t *testing.T, mr string) {
			p := filepath.Join(mr, secretsEnvPath)
			must(t, os.Rename(p, p+".real"))
			must(t, os.Symlink("secrets.env.real", p))
		}},
		{name: "directory is a link inside the mount", tamper: func(t *testing.T, mr string) {
			dir := filepath.Join(mr, "upper/etc/faas")
			must(t, os.Rename(dir, filepath.Join(mr, "upper/elsewhere")))
			must(t, os.Symlink("../elsewhere", dir))
		}},
		{name: "directory is a link out of the mount", tamper: func(t *testing.T, mr string) {
			dir := filepath.Join(mr, "upper/etc/faas")
			must(t, os.Rename(dir, filepath.Join(outside, "faas")))
			must(t, os.Symlink(filepath.Join(outside, "faas"), dir))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mountRoot := t.TempDir()
			writers, _ := learnInputs.writers(t)
			for _, w := range writers {
				must(t, w.write(mountRoot))
			}
			if tc.tamper != nil {
				tc.tamper(t, mountRoot)
			}
			if got := preBootFilesPresent(mountRoot, writers); got != tc.want {
				t.Fatalf("present = %v, want %v", got, tc.want)
			}
		})
	}
	if preBootFilesPresent(t.TempDir(), nil) {
		t.Fatal("no writers reported as present")
	}
}

// Every writer describes the file it leaves behind, so what one restore
// writes is recognised as present by the next — including a full-rootfs
// layer, where files live at the drive root rather than under upper/.
func TestPreBootFileWriters_WhatTheyWriteIsPresent(t *testing.T) {
	workloads := []WorkloadSpec{
		{Name: "main", Type: "app", RamMB: 256, Port: 8080, Essential: true},
		{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9100, preparedEnvJSON: []byte(`{"A":"1"}`)},
	}
	for _, fullRootfs := range []bool{false, true} {
		t.Run(fmt.Sprintf("full_rootfs=%v", fullRootfs), func(t *testing.T) {
			mountRoot := t.TempDir()
			if fullRootfs {
				marker := filepath.Join(mountRoot, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
				must(t, os.MkdirAll(filepath.Dir(marker), 0o755))
				must(t, os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0o644))
			}
			writers, _, err := preBootFileWriters(workloads, []byte(`{"S":"x"}`), []byte(`{"K":"v"}`), "10.100.0.1", true)
			if err != nil {
				t.Fatal(err)
			}
			if len(writers) != 7 {
				t.Fatalf("writers = %d, want 7 (update this test with the new writer)", len(writers))
			}
			if preBootFilesPresent(mountRoot, writers) {
				t.Fatal("empty drive reported as holding the files")
			}
			for _, w := range writers {
				must(t, w.write(mountRoot))
			}
			if !preBootFilesPresent(mountRoot, writers) {
				for _, w := range writers {
					if !preBootFilesPresent(mountRoot, []preBootFileWriter{w}) {
						t.Errorf("%s: written file not recognised (path %q)", w.what, w.path)
					}
				}
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
