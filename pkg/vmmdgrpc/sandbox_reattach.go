package vmmdgrpc

// adr: 20261122132055 §7 — a persistent session's VM outlives a lost stream
// (schedd or agentd restarted): vmmd keeps it running, its data-plane calls
// served, for SandboxReattachGraceSec. A start with reattach on the same
// instance and session takes it over; after the grace it is destroyed as a
// lost stream's VM always was.

import (
	"context"
	"sync"
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
// a VM a pause already destroyed, one its owner closed, and on a stopping
// vmmd; the caller destroys those.
func (s *Server) detachSandbox(wake fcvm.SandboxWakeRequest, act *sandboxActivity, vmm SandboxVMMAPI, method vmmdpb.WakeMethod) bool {
	if act.gone.Load() || act.closing.Load() {
		return false
	}
	s.sandboxDetachMu.Lock()
	if s.sandboxStopping {
		s.sandboxDetachMu.Unlock()
		return false
	}
	act.detached.Store(true)
	d := &detachedSandbox{wake: wake, act: act, vmm: vmm, method: method}
	s.sandboxDetached.Store(wake.Instance, d)
	s.sandboxDetachMu.Unlock()
	grace := s.reattachGrace()
	time.AfterFunc(grace, func() {
		// A reattach took it over, or a later detach replaced it.
		if !s.sandboxDetached.CompareAndDelete(wake.Instance, d) {
			return
		}
		s.destroyDetached(context.Background(), d)
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

// dropDetachedSandbox destroys a detached VM of instance, if one waits.
func (s *Server) dropDetachedSandbox(ctx context.Context, instance string) {
	v, ok := s.sandboxDetached.LoadAndDelete(instance)
	if !ok {
		return
	}
	s.destroyDetached(ctx, v.(*detachedSandbox))
}

func (s *Server) destroyDetached(ctx context.Context, d *detachedSandbox) {
	destroyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxDestroyTimeout)
	defer cancel()
	_ = d.vmm.Destroy(destroyCtx, d.wake.Instance)
	s.endSandboxActivity(d.wake.Instance, d.act)
	if s.log != nil {
		s.log.Info("vmmd: detached sandbox destroyed", "instance", d.wake.Instance, "sandbox_id", d.wake.SandboxID)
	}
}

// StopDetachingSandboxes is called when vmmd stops: sessions are not
// adopted, so no VM is kept for a reattach from here on, and those kept
// already are destroyed. It returns how many it destroyed.
func (s *Server) StopDetachingSandboxes(ctx context.Context) int {
	s.sandboxDetachMu.Lock()
	s.sandboxStopping = true
	s.sandboxDetachMu.Unlock()
	var kept []*detachedSandbox
	s.sandboxDetached.Range(func(k, _ any) bool {
		if v, ok := s.sandboxDetached.LoadAndDelete(k); ok {
			kept = append(kept, v.(*detachedSandbox))
		}
		return true
	})
	// In parallel: the stop must fit systemd's stop timeout.
	var wg sync.WaitGroup
	for _, d := range kept {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.destroyDetached(ctx, d)
		}()
	}
	wg.Wait()
	return len(kept)
}

// DetachedSandboxes counts the VMs kept for a reattach.
func (s *Server) DetachedSandboxes() int {
	n := 0
	s.sandboxDetached.Range(func(_, _ any) bool { n++; return true })
	return n
}
