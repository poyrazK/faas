package e2etest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// KillVMMD injects owner process death and joins Wait before any replacement.
// Native process recovery, not the harness, owns retirement of its old guests.
func (h *Harness) KillVMMD() error {
	if h == nil {
		return fmt.Errorf("e2etest: nil harness")
	}
	for _, p := range h.procs {
		if p == nil || p.Process == nil || filepath.Base(p.Path) != "vmmd" {
			continue
		}
		if p.ProcessState == nil {
			if err := p.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("kill vmmd: %w", err)
			}
			var exit *exec.ExitError
			if err := p.Wait(); err != nil && !errors.As(err, &exit) {
				return fmt.Errorf("reap vmmd: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("e2etest: vmmd process not found")
}

// RestartVMMD uses the exact original config, journal and env; a new owner
// incarnation must recover the previous physical generation through vmmd.
func (h *Harness) RestartVMMD() error {
	if h == nil || h.T == nil || len(h.vmmdLaunchEnv) == 0 || h.VMMDSock == "" {
		return fmt.Errorf("e2etest: vmmd launch recipe unavailable")
	}
	// Validate before killing: an externally supplied socket is not ours to replace.
	if h.SockDir == "" || filepath.Dir(h.VMMDSock) != h.SockDir {
		return fmt.Errorf("e2etest: vmmd socket is not harness-owned")
	}
	if err := h.KillVMMD(); err != nil {
		return err
	}
	procs := h.procs[:0]
	for _, p := range h.procs {
		if p != nil && filepath.Base(p.Path) == "vmmd" && p.ProcessState != nil {
			continue
		}
		procs = append(procs, p)
	}
	h.procs = procs
	if err := os.Remove(h.VMMDSock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	p := startProc(h.T, h.BinDir, "vmmd", append([]string(nil), h.vmmdLaunchEnv...))
	h.procs = append(h.procs, p)
	waitUnix(h.T, h.VMMDSock, 30*time.Second)
	if name, output, dead := firstDeadDaemon([]*exec.Cmd{p}); dead {
		return fmt.Errorf("e2etest: replacement %s exited: %s", name, output)
	}
	return nil
}
