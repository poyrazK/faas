package main

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const cloneWorkerCgroup = "/faas-cp.slice/faas-apid-clone-worker.service"

// Check the kernel's actual leaf, not operator environment claims. Children
// inherit this non-delegated cgroup, so pg_dump/pg_restore share its caps.
// The public APID process cannot acquire this worker's host authority.
func checkProjectEnvironmentCloneWorkerResources(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return managedpostgres.ErrUnavailable
	}
	if os.Geteuid() == 0 {
		return managedpostgres.ErrConflict
	}
	return checkCloneWorkerResources(ctx, os.ReadFile)
}

func checkCloneWorkerResources(ctx context.Context, read func(string) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := read("/proc/self/cgroup")
	if err != nil || strings.TrimSpace(string(before)) != "0::"+cloneWorkerCgroup {
		return managedpostgres.ErrConflict
	}
	root := "/sys/fs/cgroup" + cloneWorkerCgroup + "/"
	cpu, err := read(root + "cpu.max")
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	memory, err := read(root + "memory.max")
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	tasks, err := read(root + "pids.max")
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	if err := validateCloneWorkerResourceLimits(string(cpu), string(memory), string(tasks)); err != nil {
		return err
	}
	after, err := read("/proc/self/cgroup")
	if err != nil || string(after) != string(before) {
		return managedpostgres.ErrConflict
	}
	return ctx.Err()
}

func validateCloneWorkerResourceLimits(cpu, memory, tasks string) error {
	fields := strings.Fields(cpu)
	if len(fields) != 2 {
		return managedpostgres.ErrConflict
	}
	quota, err := strconv.ParseUint(fields[0], 10, 64)
	period, periodErr := strconv.ParseUint(fields[1], 10, 64)
	// Kernel CPU periods are 1 ms..1 s. Bound the operands before multiplying
	// so malformed or saturated values cannot wrap into a smaller allowance.
	if err != nil || periodErr != nil || quota == 0 || period < 1000 || period > 1000000 ||
		quota > period || quota*1000 > period*api.PostgresCopyWorkerCPUMillicoresMax {
		return managedpostgres.ErrConflict
	}
	mem, err := strconv.ParseInt(strings.TrimSpace(memory), 10, 64)
	pids, pidsErr := strconv.ParseInt(strings.TrimSpace(tasks), 10, 64)
	if err != nil || pidsErr != nil || mem <= 0 || mem > api.PostgresCopyWorkerMemoryMaxBytes || pids <= 0 || pids > api.PostgresCopyWorkerTasksMax {
		return managedpostgres.ErrConflict
	}
	return nil
}
