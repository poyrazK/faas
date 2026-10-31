package vmmdgrpc

import (
	"context"
	"errors"
	"sync"
)

// errInstanceStopped is the cancellation cause vmmd attaches to a forward
// session whose instance it destroyed, parked or stopped. Once the guest is
// gone nothing inside the network namespace answers or resets the bridge's
// TCP connection, so without an explicit close an upgraded WebSocket or a
// private TCP session stays half-open until its 24-hour ceiling: the client
// sees no close and its sends go nowhere (production-us hunt #9, H9-129).
var errInstanceStopped = errors.New("instance stopped")

// forwardSessions tracks the long-lived forward streams (raw upgrades and
// private TCP) of each instance so instance teardown can end them. The zero
// value is ready to use.
type forwardSessions struct {
	mu       sync.Mutex
	next     uint64
	sessions map[string]map[uint64]context.CancelCauseFunc
}

// track derives the context one forward stream of instance runs under. The
// returned release must run when the stream ends. Callers track before they
// check that the instance is live: a teardown that finishes first makes the
// check fail, and one that finishes later cancels the tracked context.
func (f *forwardSessions) track(ctx context.Context, instance string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	f.mu.Lock()
	if f.sessions == nil {
		f.sessions = make(map[string]map[uint64]context.CancelCauseFunc)
	}
	f.next++
	id := f.next
	if f.sessions[instance] == nil {
		f.sessions[instance] = make(map[uint64]context.CancelCauseFunc)
	}
	f.sessions[instance][id] = cancel
	f.mu.Unlock()
	return ctx, func() {
		f.mu.Lock()
		if byID := f.sessions[instance]; byID != nil {
			delete(byID, id)
			if len(byID) == 0 {
				delete(f.sessions, instance)
			}
		}
		f.mu.Unlock()
		cancel(nil)
	}
}

// closeInstance ends every tracked forward stream of instance and reports
// how many it ended.
func (f *forwardSessions) closeInstance(instance string) int {
	f.mu.Lock()
	cancels := f.sessions[instance]
	delete(f.sessions, instance)
	f.mu.Unlock()
	for _, cancel := range cancels {
		cancel(errInstanceStopped)
	}
	return len(cancels)
}

// instanceStopped reports whether ctx ended because vmmd tore its instance
// down.
func instanceStopped(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errInstanceStopped)
}

// forgetForwarding drops an ended instance's request bridge and closes its
// long-lived forward streams so their clients see the connection end.
func (s *Server) forgetForwarding(ctx context.Context, instance string) {
	s.streamBridges.forget(context.WithoutCancel(ctx), instance)
	s.closeForwardSessions(instance)
}

// suspendForwarding is forgetForwarding for a guest frozen in place
// (ADR-1151): its bridge closes, but what started it stays for the thaw.
func (s *Server) suspendForwarding(ctx context.Context, instance string) {
	s.streamBridges.suspend(context.WithoutCancel(ctx), instance)
	s.closeForwardSessions(instance)
}

func (s *Server) closeForwardSessions(instance string) {
	if n := s.forwardSessions.closeInstance(instance); n > 0 && s.log != nil {
		s.log.Info("vmmd: closed forward streams of a stopped instance", "instance", instance, "streams", n)
	}
}
