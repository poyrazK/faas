// adr: 20261101160231

package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrSnapshotVCPUMismatch: a restored VM came up with another guest vCPU count
// than the wake asked for. Firecracker restores the count the snapshot was
// captured with, so an app whose topology changed (ADR-20261101160231) would
// otherwise keep its old count on every restore. The restore is refused, the
// wake cold-boots, and schedd retires the snapshot.
var ErrSnapshotVCPUMismatch = errors.New("fcvm: restored vCPU count differs from the wake's")

// vcpuThreadPrefix names Firecracker's vCPU threads ("fc_vcpu 0", ...): one
// per guest vCPU, created while /snapshot/load builds the VM.
const vcpuThreadPrefix = "fc_vcpu"

// checkRestoredVCPUs compares the restored Firecracker's vCPU threads with
// want. It is best-effort: an unknown process or an unreadable /proc keeps
// the restore, because the count is only checked where it can be read.
func (v *JailerVMM) checkRestoredVCPUs(l Lease, want int) error {
	if want <= 0 {
		return nil
	}
	pid, ok := v.InstancePID(l.Instance)
	if !ok {
		return nil
	}
	got, err := vcpuThreadCount("/proc", pid)
	if err != nil {
		return nil //nolint:nilerr // unreadable threads keep the restore; see above
	}
	return restoredVCPUsMatch(got, want)
}

// restoredVCPUsMatch is nil when the restored VM's vCPU count is want, or when
// no vCPU thread was seen (the count is unknown).
func restoredVCPUsMatch(got, want int) error {
	if got == 0 || want <= 0 || got == want {
		return nil
	}
	return fmt.Errorf("%w: snapshot has %d, wake asked for %d", ErrSnapshotVCPUMismatch, got, want)
}

// vcpuThreadCount counts pid's threads named fc_vcpu* under procRoot.
func vcpuThreadCount(procRoot string, pid int) (int, error) {
	dir := filepath.Join(procRoot, strconv.Itoa(pid), "task")
	tasks, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, task := range tasks {
		comm, err := os.ReadFile(filepath.Join(dir, task.Name(), "comm")) //nolint:gosec // procfs of vmmd's own Firecracker child
		if err != nil {
			continue // the thread exited
		}
		if strings.HasPrefix(strings.TrimSpace(string(comm)), vcpuThreadPrefix) {
			n++
		}
	}
	return n, nil
}
