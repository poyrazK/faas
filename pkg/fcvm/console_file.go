package fcvm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
func openConsoleFile(path string) *os.File {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
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
