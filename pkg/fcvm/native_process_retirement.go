package fcvm

import (
	"context"
	"errors"
	"fmt"
	"syscall"
)

// A handle pins a kernel task. Numeric PID signals are insufficient here: a
// daemon restart can lose its os.Process while the kernel recycles that PID.
type nativeProcessHandle interface {
	Signal(syscall.Signal) error
	Wait(context.Context) error
	Close() error
}

type nativeProcessRetirer struct {
	probe nativeProcessProbe
	open  func(int) (nativeProcessHandle, error)
}

// stopObserved retires every observed incarnation of an exact instance ID.
// The returned identities remain useful for reserving their slots after an
// error. This is deliberately NOT an absence or qualification receipt: a
// durable launch fence must first exclude a child that has not exec'd yet.
func (r nativeProcessRetirer) stopObserved(ctx context.Context, instance string) ([]nativeProcessIdentity, error) {
	if !validNativeInstanceName(instance) {
		return nil, errors.New("native recovery: invalid instance identity")
	}
	processes, err := r.probe.processes(ctx)
	if err != nil {
		return nil, err
	}
	var targets []nativeProcessIdentity
	for _, process := range processes {
		if process.Instance == instance {
			targets = append(targets, process)
		}
	}
	for _, target := range targets {
		if err := r.stopProcess(ctx, target); err != nil {
			return targets, err
		}
	}
	return targets, nil
}

func (r nativeProcessRetirer) stopProcess(ctx context.Context, target nativeProcessIdentity) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	open := r.open
	if open == nil {
		open = openNativeProcess
	}
	handle, err := open(target.PID)
	if errors.Is(err, syscall.ESRCH) {
		// pidfd_open establishes that this task is already gone. A recycled
		// PID successfully opens and must pass the incarnation check below.
		return nil
	}
	if err != nil {
		return fmt.Errorf("native recovery: pin instance %s process %d: %w", target.Instance, target.PID, err)
	}
	defer func() { err = errors.Join(err, handle.Close()) }()
	current, found, err := r.probe.process(target.PID)
	if err != nil {
		return fmt.Errorf("native recovery: revalidate process %d: %w", target.PID, err)
	}
	if found {
		// Jailer may exec Firecracker and drop UID while retaining the same
		// task. Its managed identity and kernel start time must stay exact.
		if !sameNativeIncarnation(target, current) {
			return fmt.Errorf("native recovery: process %d incarnation changed", target.PID)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := handle.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("native recovery: kill instance %s process %d: %w", target.Instance, target.PID, err)
		}
	}
	// A successful signal (including ESRCH) is not an exit acknowledgement.
	// The pinned handle must report that the entire task has exited.
	if err := handle.Wait(ctx); err != nil {
		return fmt.Errorf("native recovery: wait for instance %s process %d exit: %w", target.Instance, target.PID, err)
	}
	return nil
}

func sameNativeIncarnation(a, b nativeProcessIdentity) bool {
	return a.PID == b.PID && a.StartTime == b.StartTime && a.Instance == b.Instance &&
		a.UID == b.UID && a.Plan == b.Plan && a.IsBuilder == b.IsBuilder
}
