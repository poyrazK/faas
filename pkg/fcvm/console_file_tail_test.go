package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestTailConsoleFollowsTheFileAndDrainsOnExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.console")
	f := openConsoleFile(path)
	if f == nil {
		t.Fatal("console file not opened")
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString("before adoption\n"); err != nil {
		t.Fatal(err)
	}
	from, _ := f.Seek(0, 1)
	var out syncBuffer
	flushed := make(chan struct{})
	done := make(chan struct{})
	tailDone := make(chan struct{})
	go func() {
		tailConsole(path, from, &out, func() error { close(flushed); return nil }, done)
		close(tailDone)
	}()
	if _, err := f.WriteString("line one\nline "); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for out.String() != "line one\nline " {
		if time.Now().After(deadline) {
			t.Fatalf("tail did not follow the file: %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Written just before exit, still delivered by the final drain.
	if _, err := f.WriteString("two\n"); err != nil {
		t.Fatal(err)
	}
	close(done)
	<-tailDone
	<-flushed
	if got := out.String(); got != "line one\nline two\n" {
		t.Fatalf("tail = %q, want the lines after the start offset only", got)
	}
}

// Logs/traces audit 2026-10-31 A7: Kill retired the ring as soon as the
// watchdog confirmed exit, racing the file-console tailer's final drain, so
// the guest's last output (up to one poll interval, and a trailing line
// without a newline) was lost on every teardown-driven exit. Kill now waits
// for the drain before it archives and closes the ring.
func TestKillDrainsFileConsoleBeforeRetiringRing(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	v.consoleLogDir = t.TempDir()
	var archived []logbuf.Line
	v.WithLogRetireCallback(func(_ string, lines []logbuf.Line) { archived = lines })
	id := "file-console"
	ring := v.registerRing(id)
	f := openConsoleFile(v.consoleLogPath(id))
	if f == nil {
		t.Fatal("console file not opened")
	}
	rec := &instanceRecord{consolePath: v.consoleLogPath(id), done: make(chan struct{}), fileConsole: true}
	v.recs[id] = rec
	v.startConsoleTail(rec, 0, &customerConsoleWriter{ring: ring})
	if _, err := f.WriteString("serving\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(ring.Snapshot(1)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("tail never reached the ring")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Let the tailer back off to its idle poll, then write the guest's
	// last output and exit, as a crash or a kill would.
	time.Sleep(3 * consoleTailIdle)
	if _, err := f.WriteString("last words\nno trailing newline"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	close(rec.done)
	if err := v.Kill(t.Context(), Lease{Instance: id}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range archived {
		got = append(got, line.Line)
	}
	if want := []string{"serving", "last words", "no trailing newline"}; !slices.Equal(got, want) {
		t.Fatalf("archived %q, want %q", got, want)
	}
}

// The wait is bounded: a tailer that never finishes cannot hold teardown.
func TestKillDoesNotWaitForeverForConsoleTail(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	v.consoleDrainWait = 20 * time.Millisecond
	id := "stuck-tail"
	v.registerRing(id)
	rec := &instanceRecord{done: make(chan struct{}), tailDone: make(chan struct{})}
	close(rec.done)
	v.recs[id] = rec
	if err := v.Kill(t.Context(), Lease{Instance: id}); err != nil {
		t.Fatal(err)
	}
	if v.LogRing(id) != nil {
		t.Fatal("ring survived teardown")
	}
}

func TestOpenConsoleFileAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.console")
	if err := os.WriteFile(path, []byte("previous boot\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	f := openConsoleFile(path)
	if f == nil {
		t.Fatal("console file not opened")
	}
	// Truncated for the new boot, then every write appends wherever the
	// descriptor's offset is: Firecracker and a vmmd reader never collide.
	if _, err := f.WriteAt([]byte("x"), 0); err == nil {
		t.Fatal("console file is not O_APPEND")
	}
	if _, err := f.WriteString("new boot\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if raw, _ := os.ReadFile(path); string(raw) != "new boot\n" {
		t.Fatalf("console = %q", raw)
	}
}
