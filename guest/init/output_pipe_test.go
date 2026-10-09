package main

import (
	"bytes"
	"os"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// production-us hunt #8: the workload's stdio pipe is owned by the workload
// user so a non-root image can reopen /dev/stderr. The pipe must still carry
// every byte to guest-init's writers before the run returns.
func TestWorkloadOutputPipeDeliversOutputBeforeFinish(t *testing.T) {
	var out lockedBuffer
	pipe, err := newWorkloadOutputPipe(&out, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The child's copy of the write end, as exec passes it.
	child, err := os.OpenFile("/dev/fd/"+itoaFD(pipe.w.Fd()), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	pipe.closeWriter()
	_, _ = child.WriteString("nginx: [emerg] example\n")
	_ = child.Close()
	pipe.finish()
	if got := out.String(); got != "nginx: [emerg] example\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestWorkloadOutputPipeFinishIsBounded(t *testing.T) {
	pipe, err := newWorkloadOutputPipe(&lockedBuffer{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A backgrounded grandchild keeps a write end open forever.
	held, err := os.OpenFile("/dev/fd/"+itoaFD(pipe.w.Fd()), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	start := time.Now()
	pipe.finish()
	if elapsed := time.Since(start); elapsed > workloadOutputDrainTimeout+time.Second {
		t.Fatalf("finish blocked %s", elapsed)
	}
}

func itoaFD(fd uintptr) string {
	const digits = "0123456789"
	if fd == 0 {
		return "0"
	}
	var b []byte
	for fd > 0 {
		b = append([]byte{digits[fd%10]}, b...)
		fd /= 10
	}
	return string(b)
}
