package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
