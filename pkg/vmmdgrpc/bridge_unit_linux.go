//go:build linux

package vmmdgrpc

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/sdunit"
)

// pidfdProcess is a bridge unit's main process held by a pidfd.
type pidfdProcess struct{ fd int }

func (p *pidfdProcess) alive() bool { return unix.PidfdSendSignal(p.fd, 0, nil, 0) == nil }

func (p *pidfdProcess) close() { _ = unix.Close(p.fd) }

// openUnitProcessReal opens u's main process and checks, after the pidfd
// pins it, that it runs in u's cgroup and in the namespace bound at
// netnsPath: a pid systemd reported cannot have been reused unnoticed.
func openUnitProcessReal(u sdunit.Unit, netnsPath string) (unitProcess, error) {
	if u.MainPID <= 1 || u.ControlGroup == "" {
		return nil, fmt.Errorf("unit %s has no main process", u.Name)
	}
	fd, err := unix.PidfdOpen(u.MainPID, 0)
	if err != nil {
		return nil, fmt.Errorf("pidfd for unit %s pid %d: %w", u.Name, u.MainPID, err)
	}
	cgroup, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", u.MainPID))
	if err == nil && strings.TrimSpace(string(cgroup)) != "0::"+u.ControlGroup {
		err = fmt.Errorf("pid %d is in cgroup %q, not %s's %q", u.MainPID, strings.TrimSpace(string(cgroup)), u.Name, u.ControlGroup)
	}
	if err == nil && !sameNamespace(netnsPath, u.MainPID) {
		err = fmt.Errorf("pid %d of unit %s is not in network namespace %s", u.MainPID, u.Name, netnsPath)
	}
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &pidfdProcess{fd: fd}, nil
}
