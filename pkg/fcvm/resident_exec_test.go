//go:build linux || darwin

// adr: 20261105100000 — resident jailer/firecracker copies on the jail tmpfs,
// and the bind_tun sub-phases that show why a first restore after idle is slow.

package fcvm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func writeExec(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile is subject to the umask; the checks read the mode.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestStageResidentExec(t *testing.T) {
	const body = "#!/bin/sh\nexit 0\n"
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, src string)
		wantErr string
	}{
		{name: "executable file", setup: func(t *testing.T, src string) { writeExec(t, src, body, 0o755) }},
		{name: "owner-only executable", setup: func(t *testing.T, src string) { writeExec(t, src, body, 0o700) }},
		{name: "not executable", setup: func(t *testing.T, src string) { writeExec(t, src, body, 0o644) }, wantErr: "not executable"},
		{name: "group writable", setup: func(t *testing.T, src string) { writeExec(t, src, body, 0o775) }, wantErr: "writable by its group or others"},
		{name: "world writable", setup: func(t *testing.T, src string) { writeExec(t, src, body, 0o757) }, wantErr: "writable by its group or others"},
		{name: "empty", setup: func(t *testing.T, src string) { writeExec(t, src, "", 0o755) }, wantErr: "empty"},
		{name: "directory", setup: func(t *testing.T, src string) {
			if err := os.Mkdir(src, 0o755); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "not a regular file"},
		{name: "missing", setup: func(*testing.T, string) {}, wantErr: "open installed executable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "jailer-v1.17.0-x86_64")
			tc.setup(t, src)
			dir := filepath.Join(t.TempDir(), residentExecDir)
			got, err := stageResidentExec(dir, src)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("stage = %v, want error containing %q", err, tc.wantErr)
				}
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Fatalf("failed staging left %d entries behind", len(entries))
				}
				return
			}
			if err != nil {
				t.Fatalf("stage: %v", err)
			}
			if want := filepath.Join(dir, "jailer-v1.17.0-x86_64"); got.path != want {
				t.Fatalf("path = %q, want %q: jailer names its chroot directory and recovery recognises a launch by this basename", got.path, want)
			}
			data, err := os.ReadFile(got.path)
			if err != nil || string(data) != body {
				t.Fatalf("copy = %q, %v; want the installed bytes", data, err)
			}
			info, err := os.Stat(got.path)
			srcInfo, srcErr := os.Stat(src)
			if err != nil || srcErr != nil || info.Mode().Perm() != srcInfo.Mode().Perm() {
				t.Fatalf("copy mode = %v, want the installed mode %v (%v, %v)", info.Mode().Perm(), srcInfo.Mode().Perm(), err, srcErr)
			}
			if os.SameFile(info, srcInfo) {
				t.Fatal("the copy is the installed file; it must be a separate inode")
			}
			if dirInfo, err := os.Stat(dir); err != nil || dirInfo.Mode().Perm() != 0o700 {
				t.Fatalf("resident dir = %v, %v; want 0700", dirInfo, err)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Fatalf("staging left %d entries, want exactly the copy", len(entries))
			}
		})
	}
}

func TestEnsureResidentDir(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, dir string)
		wantErr string
	}{
		{name: "absent is created", setup: func(*testing.T, string) {}},
		{name: "loose permissions are tightened", setup: func(t *testing.T, dir string) {
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a file is refused", setup: func(t *testing.T, dir string) { writeExec(t, dir, "x", 0o600) }, wantErr: "not a directory"},
		{name: "a symlink is refused", setup: func(t *testing.T, dir string) {
			if err := os.Symlink(t.TempDir(), dir); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), residentExecDir)
			tc.setup(t, dir)
			err := ensureResidentDir(dir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ensure = %v, want error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ensure: %v", err)
			}
			if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("dir = %v, %v; want 0700", info, err)
			}
		})
	}
}

func TestResidentExecsResolve(t *testing.T) {
	for _, tc := range []struct {
		name string
		// change is applied between the first and second resolve.
		change     func(t *testing.T, src, staged string)
		wantStaged bool // whether the second resolve copies again
	}{
		{name: "unchanged installed file is reused", change: func(*testing.T, string, string) {}},
		{name: "rewritten installed file is copied again", wantStaged: true, change: func(t *testing.T, src, _ string) {
			writeExec(t, src, "#!/bin/sh\nexit 1 # a new release\n", 0o755)
			later := time.Now().Add(time.Hour)
			if err := os.Chtimes(src, later, later); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "replaced installed file is copied again", wantStaged: true, change: func(t *testing.T, src, _ string) {
			next := src + ".new"
			writeExec(t, next, "#!/bin/sh\nexit 0\n", 0o755) // same size and mode, new inode
			if err := os.Rename(next, src); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "deleted copy is made again", wantStaged: true, change: func(t *testing.T, _, staged string) {
			if err := os.Remove(staged); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "swapped copy is made again", wantStaged: true, change: func(t *testing.T, _, staged string) {
			if err := os.Remove(staged); err != nil {
				t.Fatal(err)
			}
			writeExec(t, staged, "#!/bin/sh\nexit 0 # not what vmmd published\n", 0o755)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "firecracker-v1.17.0-x86_64")
			writeExec(t, src, "#!/bin/sh\nexit 0\n", 0o755)
			dir := filepath.Join(t.TempDir(), residentExecDir)
			var r residentExecs
			path, staged, err := r.resolve(dir, "firecracker", src)
			if err != nil || !staged {
				t.Fatalf("first resolve = %q, staged=%v, %v; want a fresh copy", path, staged, err)
			}
			tc.change(t, src, path)
			again, staged, err := r.resolve(dir, "firecracker", src)
			if err != nil {
				t.Fatalf("second resolve: %v", err)
			}
			if staged != tc.wantStaged {
				t.Fatalf("second resolve staged=%v, want %v", staged, tc.wantStaged)
			}
			if again != path {
				t.Fatalf("path moved from %q to %q", path, again)
			}
			want, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(again); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("copy = %q, %v; want the installed bytes %q", got, err, want)
			}
		})
	}
}

func counterValue(t *testing.T, m *WakePhaseMetrics, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, metric := range f.GetMetric() {
			if labelsMatch(metric.GetLabel(), labels) {
				return metric.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("no series %s%v", name, labels)
	return 0
}

func labelsMatch(have []*dto.LabelPair, want map[string]string) bool {
	for _, l := range have {
		if want[l.GetName()] != l.GetValue() {
			return false
		}
	}
	return len(have) == len(want)
}

func TestResidentExecutable(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        os.FileMode
		chrootBase  func(t *testing.T) string
		wantStaged  bool
		wantOutcome string
	}{
		{name: "healthy install is launched from the copy", mode: 0o755, wantStaged: true, wantOutcome: "staged"},
		{name: "group-writable install falls back to the installed file", mode: 0o775, wantOutcome: "fallback"},
		{name: "unusable jail base falls back to the installed file", mode: 0o755, wantOutcome: "fallback", chrootBase: func(t *testing.T) string {
			blocker := filepath.Join(t.TempDir(), "not-a-dir")
			writeExec(t, blocker, "x", 0o600)
			return filepath.Join(blocker, "jail")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "jailer-v1.17.0-x86_64")
			writeExec(t, src, "#!/bin/sh\nexit 0\n", tc.mode)
			base := t.TempDir()
			if tc.chrootBase != nil {
				base = tc.chrootBase(t)
			}
			m := NewWakePhaseMetrics()
			v := NewJailerVMM(base, time.Second).WithWakePhaseMetrics(m)
			got := v.residentExecutable("jailer", src)
			if tc.wantStaged {
				if want := filepath.Join(base, residentExecDir, "jailer-v1.17.0-x86_64"); got != want {
					t.Fatalf("launch path = %q, want the resident copy %q", got, want)
				}
			} else if got != src {
				t.Fatalf("launch path = %q, want the installed %q: staging must never make a wake fail", got, src)
			}
			v.residentExecutable("jailer", src) // a second launch does not copy or fall back twice more than it must
			if got := counterValue(t, m, "vmmd_resident_exec_stages_total", map[string]string{"binary": "jailer", "outcome": tc.wantOutcome}); got < 1 {
				t.Fatalf("%s count = %v, want at least 1", tc.wantOutcome, got)
			}
			if tc.wantStaged {
				if got := counterValue(t, m, "vmmd_resident_exec_stages_total", map[string]string{"binary": "jailer", "outcome": "staged"}); got != 1 {
					t.Fatalf("staged count after two launches = %v, want 1: the second launch must reuse the copy", got)
				}
			}
		})
	}
	var nilVMM *JailerVMM
	if got := nilVMM.residentExecutable("jailer", "/usr/local/bin/jailer"); got != "/usr/local/bin/jailer" {
		t.Fatalf("nil VMM path = %q", got)
	}
	// Without a jail base there is nowhere to stage.
	if got := (&JailerVMM{}).residentExecutable("jailer", "/usr/local/bin/jailer"); got != "/usr/local/bin/jailer" {
		t.Fatalf("no-jail-base path = %q", got)
	}
}

func TestFirstWarningLogsEachFailureOnce(t *testing.T) {
	var r residentExecs
	a, b := errors.New("a"), errors.New("b")
	for i, tc := range []struct {
		kind string
		err  error
		want bool
	}{
		{"jailer", a, true}, {"jailer", a, false}, {"jailer", b, true}, {"jailer", b, false},
		{"firecracker", a, true}, {"jailer", a, true},
	} {
		if got := r.firstWarning(tc.kind, tc.err); got != tc.want {
			t.Fatalf("step %d (%s, %v) = %v, want %v", i, tc.kind, tc.err, got, tc.want)
		}
	}
}

func TestPrepareResidentBinaries(t *testing.T) {
	bin := t.TempDir()
	writeExec(t, filepath.Join(bin, "jailer-v1.17.0-x86_64"), "jailer", 0o755)
	writeExec(t, filepath.Join(bin, "firecracker-v1.17.0-x86_64"), "firecracker", 0o755)
	for link, target := range map[string]string{"jailer": "jailer-v1.17.0-x86_64", "firecracker": "firecracker-v1.17.0-x86_64"} {
		if err := os.Symlink(filepath.Join(bin, target), filepath.Join(bin, link)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	base := t.TempDir()
	dir := filepath.Join(base, residentExecDir)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"firecracker-v1.7.0-x86_64", "jailer-v1.7.0", residentExecTempPrefix + "crashed"} {
		writeExec(t, filepath.Join(dir, stale), "old", 0o755)
	}

	v := NewJailerVMM(base, time.Second)
	v.PrepareResidentBinaries()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got, want := strings.Join(names, ","), "firecracker-v1.17.0-x86_64,jailer-v1.17.0-x86_64"; got != want {
		t.Fatalf("resident dir = %s, want %s: versioned copies are what jailer and recovery key on, and old versions are pruned", got, want)
	}
	// The next launch finds both copies already current.
	m := NewWakePhaseMetrics()
	v.wakePhaseMetrics = m
	jailer, err := resolveExecutable("jailer")
	if err != nil {
		t.Fatal(err)
	}
	if got := v.residentExecutable("jailer", jailer); got != filepath.Join(dir, "jailer-v1.17.0-x86_64") {
		t.Fatalf("launch path = %q", got)
	}
	if got := counterValue(t, m, "vmmd_resident_exec_stages_total", map[string]string{"binary": "jailer", "outcome": "staged"}); got != 0 {
		t.Fatalf("first launch after PrepareResidentBinaries staged %v times, want 0", got)
	}
}

// A launch execs the resident jailer and hands it the resident firecracker, and
// both keep the installed basenames, so jailer builds the same chroot path and
// native recovery still recognises the process (nativeExecutableName).
func TestStartJailerLaunchesResidentCopies(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "argv")
	writeExec(t, filepath.Join(bin, "jailer-v1.17.0-x86_64"), "#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\" > \"$RESIDENT_MARKER\"\n", 0o755)
	writeExec(t, filepath.Join(bin, "firecracker-v1.17.0-x86_64"), "#!/bin/sh\nexit 0\n", 0o755)
	for link, target := range map[string]string{"jailer": "jailer-v1.17.0-x86_64", "firecracker": "firecracker-v1.17.0-x86_64"} {
		if err := os.Symlink(filepath.Join(bin, target), filepath.Join(bin, link)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("RESIDENT_MARKER", marker)
	base := t.TempDir()
	v := NewJailerVMM(base, time.Second)
	l := leaseForSlot("resident-launch", 0)
	if err := v.startJailer(t.Context(), l); err != nil {
		t.Fatalf("startJailer: %v", err)
	}
	v.mu.Lock()
	rec := v.recs[l.Instance]
	v.mu.Unlock()
	select {
	case <-rec.done:
	case <-time.After(5 * time.Second):
		t.Fatal("fake jailer did not exit")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("fake jailer did not run: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	resident := filepath.Join(base, residentExecDir)
	if got, want := args[0], filepath.Join(resident, "jailer-v1.17.0-x86_64"); got != want {
		t.Fatalf("jailer launched as %q, want the resident copy %q", got, want)
	}
	var execFile string
	for i, a := range args {
		if a == "--exec-file" && i+1 < len(args) {
			execFile = args[i+1]
		}
	}
	if want := filepath.Join(resident, "firecracker-v1.17.0-x86_64"); execFile != want {
		t.Fatalf("--exec-file = %q, want the resident copy %q", execFile, want)
	}
	if !nativeExecutableName(filepath.Base(args[0]), "jailer") || !nativeExecutableName(filepath.Base(execFile), "firecracker") {
		t.Fatal("resident copies are not recognised as a jailer launch by native recovery")
	}
	if got := resolveFCChrootName(); got != "firecracker-v1.17.0-x86_64" {
		t.Fatalf("chroot name = %q; the resident copy must not change where jailer builds the chroot", got)
	}
}

type waitsInputs struct {
	nativeJailDeviceInputs
	mntns, chroot time.Duration
}

func (w waitsInputs) JailWaits() (time.Duration, time.Duration) { return w.mntns, w.chroot }

func TestNativeDeviceTimingsPartition(t *testing.T) {
	start := time.Unix(1000, 0)
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	for _, tc := range []struct {
		name                  string
		prepareAt             time.Duration // zero: setup never reached the jailer wait
		mntns, chroot         time.Duration
		total                 time.Duration
		checks, wm, wc, setup int64
	}{
		{name: "fast restore", prepareAt: ms(2), mntns: ms(1), chroot: ms(3), total: ms(11), checks: 2, wm: 1, wc: 3, setup: 5},
		{name: "slow jailer is attributed to its waits", prepareAt: ms(2), mntns: ms(30), chroot: ms(140), total: ms(223), checks: 2, wm: 30, wc: 140, setup: 51},
		{name: "slow journal checks are not charged to the jailer", prepareAt: ms(180), mntns: 0, chroot: ms(1), total: ms(200), checks: 180, wm: 0, wc: 1, setup: 19},
		{name: "failed before the jailer wait", total: ms(7), checks: 7},
		{name: "waits longer than the window cannot go negative", prepareAt: ms(1), mntns: ms(10), chroot: ms(10), total: ms(12), checks: 1, wm: 10, wc: 10, setup: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capture nativeDeviceTimings
			if tc.prepareAt > 0 {
				capture.notePrepare(start.Add(tc.prepareAt), waitsInputs{mntns: tc.mntns, chroot: tc.chroot})
			}
			got := capture.partition(start, start.Add(tc.total))
			want := bindTunTimings{JournalChecksMs: tc.checks, WaitMntnsMs: tc.wm, WaitChrootMs: tc.wc, SetupJailMs: tc.setup}
			if got != want {
				t.Fatalf("partition = %+v, want %+v", got, want)
			}
		})
	}
	var nilCapture *nativeDeviceTimings
	nilCapture.notePrepare(start, nil) // a helper that is not a device setup passes no capture
	if got := nilCapture.partition(start, start.Add(ms(4))); got.JournalChecksMs != 4 {
		t.Fatalf("nil capture = %+v", got)
	}
	var capture nativeDeviceTimings
	capture.notePrepare(start, nil) // a failed Prepare returns no inputs
	if capture.waitMntns != 0 || capture.waitChroot != 0 {
		t.Fatalf("waits from nil inputs = %v %v", capture.waitMntns, capture.waitChroot)
	}
}
