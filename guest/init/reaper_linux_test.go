//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Hunt 34 H34-35: the PID 1 reaper collected a companion's exit before the
// companion's own exec.Cmd.Wait did, and the crash read "waitid: no child
// processes" instead of "exit status 127". A Wait racing the reaper must keep
// its child's exit status.
func TestReapOneLeavesAWaitedChildToItsWait(t *testing.T) {
	for run := 0; run < 200; run++ {
		cmd := exec.Command("/bin/sh", "-c", "exit 7")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			for {
				select {
				case <-stop:
					return
				default:
					reapOne(nil)
				}
			}
		}()
		err := cmd.Wait()
		close(stop)
		<-stopped
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
			t.Fatalf("run %d: Wait = %v, want exit status 7", run, err)
		}
	}
}

// A child nobody waits for, like an orphan re-parented to PID 1, is still
// reaped. syscall.ForkExec takes no pidfd.
func TestReapOneReapsAChildNobodyWaitsFor(t *testing.T) {
	pid, err := syscall.ForkExec("/bin/sh", []string{"sh", "-c", "exit 0"}, &syscall.ProcAttr{})
	if err != nil {
		t.Fatal(err)
	}
	stat := filepath.Join("/proc", fmt.Sprint(pid), "stat")
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(stat)
		if err != nil {
			t.Fatalf("child %d vanished before reapOne ran: %v", pid, err)
		}
		if state, _, ok := parseProcStat(raw); ok && state == "Z" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child %d never exited", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	reapOne(nil)
	if _, err := os.Stat(stat); !os.IsNotExist(err) {
		t.Fatalf("child %d still present after reapOne: %v", pid, err)
	}
}

func TestParseProcStat(t *testing.T) {
	for _, tc := range []struct {
		stat      string
		state     string
		ppid      int
		wantParse bool
	}{
		{"42 (node) Z 1 42 42 0 -1", "Z", 1, true},
		{"43 (my (odd) name) S 7 43 43 0 -1", "S", 7, true},
		{"44 (a b) R", "", 0, false},
		{"garbage", "", 0, false},
	} {
		state, ppid, ok := parseProcStat([]byte(tc.stat))
		if ok != tc.wantParse || state != tc.state || ppid != tc.ppid {
			t.Errorf("parseProcStat(%q) = %q, %d, %v; want %q, %d, %v", tc.stat, state, ppid, ok, tc.state, tc.ppid, tc.wantParse)
		}
	}
}

func TestPidfdTargetsReadsOnlyLivePidfds(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"3": "pos:\t0\nflags:\t02000002\nmnt_id:\t15\nino:\t1057\nPid:\t1234\nNSpid:\t1234\n",
		"4": "pos:\t0\nflags:\t02000002\nmnt_id:\t15\nino:\t1058\nPid:\t-1\nNSpid:\t-1\n",
		"5": "pos:\t0\nflags:\t02\nmnt_id:\t21\nino:\t99\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := pidfdTargets(dir)
	if len(got) != 1 || !got[1234] {
		t.Fatalf("pidfdTargets = %v, want only 1234", got)
	}
}
