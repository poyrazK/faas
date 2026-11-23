//go:build !windows

package main

import (
	"strings"
	"testing"
	"time"
)

func TestReleaseInstallLockWaitIsBounded(t *testing.T) {
	wait, poll := releaseInstallLockWait, releaseInstallLockPoll
	releaseInstallLockWait, releaseInstallLockPoll = 50*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { releaseInstallLockWait, releaseInstallLockPoll = wait, poll })

	root := t.TempDir()
	unlock, err := lockReleaseInstalls(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockReleaseInstalls(root); err == nil || !strings.Contains(err.Error(), "still held") {
		t.Fatalf("second lock err = %v, want a bounded wait failure", err)
	}
	unlock()
	again, err := lockReleaseInstalls(root)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}
