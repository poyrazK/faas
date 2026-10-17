package e2etest

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// RestartGateway and RestartVMMD fault only the owned harness children. The
// exact launch environment/configuration is retained; guests remain daemon-owned.
func (h *Harness) RestartGateway() error { return h.restartNativeOwner("gatewayd-internal") }

func (h *Harness) markDaemonStopped(child *exec.Cmd) {
	if h.expectedStops == nil {
		h.expectedStops = map[*exec.Cmd]bool{}
	}
	h.expectedStops[child] = true
}

func (h *Harness) watchedDaemons() []*exec.Cmd {
	var watched []*exec.Cmd
	for _, child := range h.procs {
		if !h.expectedStops[child] {
			watched = append(watched, child)
		}
	}
	return watched
}

func (h *Harness) restartNativeOwner(name string) error {
	if h == nil || h.T == nil || h.BinDir == "" || (name != "vmmd" && name != "gatewayd-internal") {
		return fmt.Errorf("e2etest: invalid native restart owner")
	}
	if name == "vmmd" && (h.SockDir == "" || filepath.Dir(h.VMMDSock) != h.SockDir) {
		return fmt.Errorf("e2etest: vmmd socket is not harness-owned")
	}
	var gatewayControlHost string
	if name == "gatewayd-internal" {
		control, err := url.Parse(h.GatewayControlURL)
		if err != nil || control.Host == "" || control.Scheme != "http" {
			return fmt.Errorf("e2etest: invalid owned gateway control URL")
		}
		gatewayControlHost = control.Host
	}
	for index, child := range h.procs {
		if child == nil || child.Process == nil || child.ProcessState != nil || child.Path != filepath.Join(h.BinDir, name) {
			continue
		}
		environment := append([]string(nil), child.Env...)
		if err := child.Process.Kill(); err != nil {
			return fmt.Errorf("e2etest: kill owned %s: %w", name, err)
		}
		if err := child.Wait(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				return fmt.Errorf("e2etest: reap %s: %w", name, err)
			}
		}
		if name == "vmmd" {
			if err := os.Remove(h.VMMDSock); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		h.procs[index] = startProc(h.T, h.BinDir, name, environment)
		if name == "vmmd" {
			waitUnix(h.T, h.VMMDSock, 30*time.Second)
		} else {
			waitReadyz(h.T, gatewayControlHost, 30*time.Second)
		}
		h.requireDaemonsAlive(h.T)
		return nil
	}
	return fmt.Errorf("e2etest: live owned %s child not found", name)
}
