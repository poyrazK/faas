//go:build linux

package fcvm

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// writeMemoryReclaim asks the kernel to reclaim n bytes from a cgroup and
// gives up after timeout. A memory.reclaim write only returns early when the
// writer has a signal pending, so the write runs on a locked thread that
// receives SIGURG at the deadline. The Go runtime already uses SIGURG for
// preemption and ignores extra ones. The kernel ends the write with EINTR,
// which the raw write returns instead of retrying; any other signal that
// lands on the thread ends it the same way.
func writeMemoryReclaim(path string, n int64, timeout time.Duration) error {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_TRUNC|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = unix.Close(fd) }()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pid, tid := unix.Getpid(), unix.Gettid()
	var mu sync.Mutex
	writing := true
	timer := time.AfterFunc(timeout, func() {
		mu.Lock()
		defer mu.Unlock()
		if writing {
			_ = unix.Tgkill(pid, tid, unix.SIGURG)
		}
	})
	_, err = unix.Write(fd, []byte(strconv.FormatInt(n, 10)))
	mu.Lock()
	writing = false
	mu.Unlock()
	timer.Stop()
	if err != nil {
		return &os.PathError{Op: "write", Path: path, Err: err}
	}
	return nil
}
