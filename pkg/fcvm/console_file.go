package fcvm

// File-backed guest consoles (vmmd restart adoption ADR §2 "Console").
//
// By default Firecracker's stdout is a pipe into vmmd, which copies it into
// the console file and the log ring. When vmmd dies the pipe's reader goes
// with it, and Firecracker stays alive but stops answering its API (fsn-4,
// V-A3, 2026-10-17), so the guest cannot be adopted. With a file console,
// Firecracker's stdout and stderr are the console file itself, and vmmd
// tails it into the log ring. Nothing Firecracker writes to depends on vmmd
// being alive, and a stalled reader can no longer back-pressure the serial
// device.

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	consoleTailBusy = 20 * time.Millisecond
	consoleTailIdle = 200 * time.Millisecond
	consoleTailRead = 32 << 10
	// consoleFileMaxBytes caps one VM's console file on the node's root
	// filesystem. Every byte a tenant app writes to stdout or stderr lands
	// there, so an app logging in a loop filled the host disk (bug hunt
	// 2026-10-28 B15). Past the cap the file starts over with a marker; the
	// bounded per-instance log ring is unaffected.
	consoleFileMaxBytes = 16 << 20
)

var consoleTruncatedMarker = []byte("\n[gregale: console file exceeded its size cap; earlier output dropped]\n")

// cappedConsoleFile is the console file vmmd writes in pipe mode, started
// over with a marker once it would pass consoleFileMaxBytes. The file is
// opened O_APPEND, so writes after Truncate land at the new end.
type cappedConsoleFile struct {
	f       *os.File
	written int64
}

func (c *cappedConsoleFile) Write(p []byte) (int, error) {
	if c.written+int64(len(p)) > consoleFileMaxBytes {
		if err := c.f.Truncate(0); err == nil {
			n, _ := c.f.Write(consoleTruncatedMarker)
			c.written = int64(n)
		}
	}
	n, err := c.f.Write(p)
	c.written += int64(n)
	return n, err
}

// removeConsoleFile deletes instance's console file once the VM is gone.
// Nothing removed them, so the directory grew by one file per wake for the
// life of the node (B15). A failed restore's preserved console has another
// name and stays.
func (v *JailerVMM) removeConsoleFile(instance string) {
	if err := os.Remove(v.consoleLogPath(instance)); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("vmm: remove console file", "instance", instance, "err", err)
	}
}

// defaultConsoleLogDir holds each VM's serial console copy,
// vm-<instance>.console.
const defaultConsoleLogDir = "/var/log/faas"

// consoleLogPath is the console file startJailer writes for instance.
func (v *JailerVMM) consoleLogPath(instance string) string {
	dir := v.consoleLogDir
	if dir == "" {
		dir = defaultConsoleLogDir
	}
	return filepath.Join(dir, "vm-"+instance+".console")
}

// consoleFileOpen is a console file being created ahead of startJailer.
type consoleFileOpen struct {
	done chan struct{}
	f    *os.File // nil when the open failed
}

// openConsoleFile creates or truncates a console file. A failure leaves the
// VM without the file copy; the per-instance ring still gets the output.
// O_APPEND lets Firecracker write a file console itself.
func openConsoleFile(path string) *os.File {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o640)
	if err != nil {
		return nil
	}
	return f
}

// preopenConsoleFile starts creating instance's console file so that
// startJailer does not wait for it. On production-us (rc.285) that create
// took 24–52 ms on the node's root filesystem (bpftrace on vmmd's openat),
// which made start_jailer the third-largest restore phase. Started at the top
// of Restore and boot, the open runs while the drives are staged. The
// returned func closes the file if startJailer never took it.
func (v *JailerVMM) preopenConsoleFile(instance string) (discard func()) {
	o := &consoleFileOpen{done: make(chan struct{})}
	path := v.consoleLogPath(instance)
	go func() {
		defer close(o.done)
		o.f = openConsoleFile(path)
	}()
	v.mu.Lock()
	if v.consoleOpens == nil {
		v.consoleOpens = make(map[string]*consoleFileOpen)
	}
	prev := v.consoleOpens[instance]
	v.consoleOpens[instance] = o
	v.mu.Unlock()
	closeConsoleFileOpen(prev)
	return func() {
		v.mu.Lock()
		if v.consoleOpens[instance] != o {
			v.mu.Unlock()
			return
		}
		delete(v.consoleOpens, instance)
		v.mu.Unlock()
		closeConsoleFileOpen(o)
	}
}

// takeConsoleFile returns instance's console file: the one
// preopenConsoleFile started, or one opened now. nil means no file.
func (v *JailerVMM) takeConsoleFile(instance string) *os.File {
	v.mu.Lock()
	o := v.consoleOpens[instance]
	delete(v.consoleOpens, instance)
	v.mu.Unlock()
	if o == nil {
		return openConsoleFile(v.consoleLogPath(instance))
	}
	<-o.done
	return o.f
}

// PreserveFailedRestoreConsole keeps a failed restore's serial console, which
// holds guest-init's resume-hook diagnosis, before the fallback cold boot of
// the same instance reopens and truncates the console log.
func (v *JailerVMM) PreserveFailedRestoreConsole(instance string) (string, error) {
	src := v.consoleLogPath(instance)
	dst := strings.TrimSuffix(src, ".console") + ".restore-failed.console"
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("vmm: keep failed restore console: %w", err)
	}
	return dst, nil
}

// closeConsoleFileOpen closes an untaken console file once its open ends,
// without making the caller wait for it.
func closeConsoleFileOpen(o *consoleFileOpen) {
	if o == nil {
		return
	}
	go func() {
		<-o.done
		if o.f != nil {
			_ = o.f.Close()
		}
	}()
}

// WithFileConsole makes every guest launched from now on write its console
// straight to its console file. vmmd turns it on with adoption records:
// only file-console guests can be adopted.
func (v *JailerVMM) WithFileConsole() *JailerVMM {
	v.mu.Lock()
	v.fileConsole = true
	v.mu.Unlock()
	return v
}

func (v *JailerVMM) fileConsoleEnabled() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fileConsole
}

// consoleIsFile reports whether instance's running guest writes its console
// to a file (and so survives this vmmd for adoption).
func (v *JailerVMM) consoleIsFile(instance string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	rec := v.recs[instance]
	return rec != nil && rec.fileConsole
}

// tailConsole copies the console file from offset into w until done closes,
// then drains what is left and flushes the last partial line.
func tailConsole(path string, from int64, w io.Writer, flush func() error, done <-chan struct{}) {
	f, err := os.Open(path)
	if err != nil {
		slog.Warn("vmm: console tail unavailable", "path", path, "err", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		slog.Warn("vmm: console tail seek", "path", path, "err", err)
		return
	}
	buf := make([]byte, consoleTailRead)
	wait := consoleTailBusy
	for {
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			wait = consoleTailBusy
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			slog.Warn("vmm: console tail read", "path", path, "err", err)
			return
		}
		// Firecracker appends to a file console itself; start the file
		// over at the cap once everything written so far was read (B15).
		if offset, serr := f.Seek(0, io.SeekCurrent); serr == nil && offset > consoleFileMaxBytes {
			if terr := os.Truncate(path, 0); terr == nil {
				_, _ = f.Seek(0, io.SeekStart)
			}
		}
		select {
		case <-done:
			for {
				n, err := f.Read(buf)
				if n > 0 {
					_, _ = w.Write(buf[:n])
				}
				if n == 0 || err != nil {
					break
				}
			}
			if flush != nil {
				_ = flush()
			}
			return
		case <-time.After(wait):
			if wait < consoleTailIdle {
				wait *= 2
			}
		}
	}
}
