package vmmdgrpc

// adr: 20261122132055 §7 — a persistent session's VM outlives a lost stream
// (schedd or agentd restarted): vmmd keeps it running, its data-plane calls
// served, for SandboxReattachGraceSec. A start with reattach on the same
// instance and session takes it over; after the grace it is destroyed as a
// lost stream's VM always was.

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// detachedSandbox is a running session VM without a stream.
type detachedSandbox struct {
	wake   fcvm.SandboxWakeRequest
	act    *sandboxActivity
	vmm    SandboxVMMAPI
	method vmmdpb.WakeMethod
}

func (s *Server) reattachGrace() time.Duration {
	if s.sandboxReattachGrace > 0 {
		return s.sandboxReattachGrace
	}
	return time.Duration(api.SandboxReattachGraceSec) * time.Second
}

// detachSandbox keeps the session's VM for a reattach. It reports false for
// a VM a pause already destroyed.
func (s *Server) detachSandbox(wake fcvm.SandboxWakeRequest, act *sandboxActivity, vmm SandboxVMMAPI, method vmmdpb.WakeMethod) bool {
	if act.gone.Load() {
		return false
	}
	act.detached.Store(true)
	d := &detachedSandbox{wake: wake, act: act, vmm: vmm, method: method}
	s.sandboxDetached.Store(wake.Instance, d)
	grace := s.reattachGrace()
	time.AfterFunc(grace, func() {
		// A reattach took it over, or a later detach replaced it.
		if !s.sandboxDetached.CompareAndDelete(wake.Instance, d) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), sandboxDestroyTimeout)
		defer cancel()
		_ = vmm.Destroy(ctx, wake.Instance)
		s.endSandboxActivity(wake.Instance, act)
		if s.log != nil {
			s.log.Info("vmmd: detached sandbox not reattached; destroyed", "instance", wake.Instance, "sandbox_id", wake.SandboxID)
		}
	})
	if s.log != nil {
		s.log.Info("vmmd: sandbox stream lost; VM kept for a reattach", "instance", wake.Instance, "sandbox_id", wake.SandboxID, "grace", grace)
	}
	return true
}

// reattachSandbox hands a detached VM to a new stream and serves it as the
// stream that booted it did.
func (s *Server) reattachSandbox(stream vmmdpb.Vmmd_RunSandboxServer, start *vmmdpb.RunSandboxStart) error {
	instance := start.GetInstance()
	v, ok := s.sandboxDetached.Load(instance)
	if !ok {
		return status.Error(codes.NotFound, "no detached VM of this session on this node")
	}
	d := v.(*detachedSandbox)
	if d.wake.SandboxID != start.GetSandboxId() || d.wake.AccountID != start.GetAccountId() {
		return status.Error(codes.NotFound, "no detached VM of this session on this node")
	}
	if !s.sandboxDetached.CompareAndDelete(instance, d) {
		return status.Error(codes.NotFound, "the detached VM was destroyed or taken over")
	}
	act := d.act
	act.detached.Store(false)
	ctx := stream.Context()
	detached := false
	defer func() {
		if detached {
			return
		}
		destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxDestroyTimeout)
		defer cancel()
		_ = d.vmm.Destroy(destroyCtx, instance)
		s.endSandboxActivity(instance, act)
	}()
	if err := stream.Send(&vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Booted{Booted: &vmmdpb.RunSandboxBooted{
		Method: d.method, Actions: act.runAction != nil, EgressPolicy: true, Reattached: true,
	}}}); err != nil {
		detached = s.detachSandbox(d.wake, act, d.vmm, d.method)
		return err
	}
	if s.log != nil {
		s.log.Info("vmmd: sandbox reattached", "instance", instance, "sandbox_id", d.wake.SandboxID)
	}
	// Activity is reported afresh to the new owner.
	act.mu.Lock()
	act.dirty = true
	act.mu.Unlock()
	err := s.serveSandbox(ctx, stream, instance, act)
	if err != nil {
		detached = s.detachSandbox(d.wake, act, d.vmm, d.method)
	}
	return err
}
