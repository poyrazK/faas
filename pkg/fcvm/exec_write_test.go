package fcvm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A helper on another filesystem than the chroots is copied beside them once,
// not on every launch: per-launch copies held the fork lock on every wake and
// cost the metal park/wake cycle about 65 ms each.
func TestLocalMountHelperIsCopiedOncePerSource(t *testing.T) {
	base := t.TempDir()
	v := &JailerVMM{chrootBase: base}
	src := filepath.Join(t.TempDir(), "vmmd")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := v.localMountHelper(src)
	if err != nil {
		t.Fatalf("localMountHelper: %v", err)
	}
	if filepath.Dir(first) != base {
		t.Fatalf("copy at %s, want it in the chroot base %s", first, base)
	}
	before, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := v.localMountHelper(src)
	if err != nil {
		t.Fatalf("localMountHelper again: %v", err)
	}
	after, err := os.Stat(again)
	if err != nil {
		t.Fatal(err)
	}
	if again != first || !os.SameFile(before, after) {
		t.Fatalf("second call made a new copy (%s), want the first one reused", again)
	}
	if after.Mode().Perm()&0o111 == 0 {
		t.Fatalf("copy mode %v is not executable", after.Mode())
	}

	other := filepath.Join(t.TempDir(), "vmmd")
	if err := os.WriteFile(other, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := v.localMountHelper(other); err != nil {
		t.Fatalf("localMountHelper(other): %v", err)
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != "#!/bin/sh\nexit 0\n" {
		t.Fatalf("a different source did not replace the copy: %q, %v", got, err)
	}
}

// A child forked while writeExecutable's file is open would inherit the write
// descriptor and make exec'ing the file fail with ETXTBSY, so a process start
// that races the write must wait until write has returned.
func TestWriteExecutableHoldsProcessStartsUntilWriteReturns(t *testing.T) {
	entered := make(chan struct{})
	started := make(chan time.Time, 1)
	go func() {
		<-entered
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		err := cmd.Start()
		started <- time.Now()
		if err == nil {
			_ = cmd.Wait()
		}
	}()

	var returned time.Time
	sentinel := errors.New("write result")
	err := writeExecutable(func() error {
		close(entered)
		select {
		case <-started:
			t.Error("a process started while the file was open for writing")
		case <-time.After(200 * time.Millisecond):
		}
		returned = time.Now()
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("writeExecutable returned %v, want write's own error", err)
	}

	select {
	case at := <-started:
		if at.Before(returned) {
			t.Fatalf("process started at %v, before write returned at %v", at, returned)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting process never started after the write returned")
	}
}
