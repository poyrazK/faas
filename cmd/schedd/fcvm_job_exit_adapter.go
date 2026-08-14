// fcvm_job_exit_adapter.go — schedd-side adapter for the
// local-host vsock UDS WaitJobExit (ADR-099 PR-C).
//
// pkg/sched.Engine.WithJobExitWaiter accepts a
// sched.LocalJobExitWaiter, which is satisfied by the
// (ctx, instance, deadline) signature. The production handle is
// a *fcvm.JailerVMM (pkg/fcvm/vmm.go); this file adapts the
// sched-side signature to the fcvm-side signature, which takes
// a Lease (struct with Instance + a few other fields).
//
// PR-C follow-up: cmd/schedd/main.go wires the actual
// *fcvm.JailerVMM handle once the socket/chroot resolution is
// factored into a single constructor. For now the adapter
// compiles + builds clean; the dispatcher tick is disabled
// (FAAS_JOBS_DISABLED=1 default) so a green PR-C build never
// accidentally exercises the un-wired path.
package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// fcvmJobExitAdapter satisfies sched.LocalJobExitWaiter on
// top of a *fcvm.JailerVMM. The vmm field is nil in PR-C's
// "compiled-but-unwired" build; once the follow-up wires the
// real handle, vmm becomes non-nil and the WaitJobExit call
// hits the per-instance vsock UDS.
type fcvmJobExitAdapter struct {
	vmm *fcvm.JailerVMM
}

// WaitJobExit (ADR-099 PR-C) translates the sched-side
// signature (ctx, instanceID, deadline) into the fcvm-side
// (ctx, lease, deadline). The Lease.Instance field is set
// from the sched-supplied instanceID; the other Lease fields
// are zero-valued because the vsock UDS path doesn't consult
// them (they belong to the lifecycle RPCs, not the DGRAM
// reader).
//
// PR-C stub: returns (-1, -1, nil) so the engine.WakeJob call
// returns JobOutcomeBootFail rather than dereferencing a nil
// vmm. The follow-up commit wires the real handle; the
// production path becomes a thin shim.
func (a *fcvmJobExitAdapter) WaitJobExit(ctx context.Context, instanceID string, deadline time.Duration) (int, int, error) {
	if a == nil || a.vmm == nil {
		return -1, -1, nil
	}
	lease := fcvm.Lease{Instance: instanceID}
	return a.vmm.WaitJobExit(ctx, lease, deadline)
}
