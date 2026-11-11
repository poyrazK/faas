// adr: 20261027090013 §11 — a live session exposes at most its plan's ports,
// only the owning account changes them, a closing session exposes none, and
// the gateway's route carries the session's placement and every exposed port.
package agents_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxPorts(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{})
	if _, err := s.ExposeSandboxPort(ctx, account, sb.ID, 3000, agents.SandboxPortPrivate, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExposeSandboxPort(ctx, account, sb.ID, 8080, agents.SandboxPortPublic, 2); err != nil {
		t.Fatal(err)
	}
	var limit *agents.LimitError
	if _, err := s.ExposeSandboxPort(ctx, account, sb.ID, 9000, agents.SandboxPortPrivate, 2); !errors.As(err, &limit) || limit.Observed != 2 {
		t.Fatalf("a third port at max 2 = %v", err)
	}
	// Exposing an exposed port again at the limit changes its visibility.
	if p, err := s.ExposeSandboxPort(ctx, account, sb.ID, 3000, agents.SandboxPortPublic, 2); err != nil || p.Visibility != agents.SandboxPortPublic {
		t.Fatalf("re-expose = %+v, %v", p, err)
	}
	if _, err := s.ExposeSandboxPort(ctx, other, sb.ID, 4000, agents.SandboxPortPrivate, 2); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account exposed a port: %v", err)
	}
	if ports, _ := s.SandboxPorts(ctx, other, sb.ID); len(ports) != 0 {
		t.Fatalf("another account lists %d ports", len(ports))
	}
	route, err := s.PreviewRoute(ctx, sb.ID)
	if err != nil || route.AccountID != account || route.Plan == "" || len(route.Ports) != 2 ||
		route.Ports[3000] != agents.SandboxPortPublic || route.Ports[8080] != agents.SandboxPortPublic {
		t.Fatalf("route = %+v, %v", route, err)
	}
	if err := s.UnexposeSandboxPort(ctx, other, sb.ID, 3000); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account removed a port: %v", err)
	}
	if err := s.UnexposeSandboxPort(ctx, account, sb.ID, 3000); err != nil {
		t.Fatal(err)
	}
	if err := s.UnexposeSandboxPort(ctx, account, sb.ID, 3000); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("removed twice: %v", err)
	}
	if ports, err := s.SandboxPorts(ctx, account, sb.ID); err != nil || len(ports) != 1 || ports[0].Port != 8080 {
		t.Fatalf("ports = %+v, %v", ports, err)
	}
	if _, err := s.CloseSandbox(ctx, account, sb.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExposeSandboxPort(ctx, account, sb.ID, 5000, agents.SandboxPortPrivate, 2); !errors.Is(err, agents.ErrSandboxNotLive) {
		t.Fatalf("a closing session exposed a port: %v", err)
	}
	if _, err := s.PreviewRoute(ctx, "not-a-uuid"); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("bad id route: %v", err)
	}
}

// Ports at create are exposed with the session, in its transaction: a
// refused session leaves no port rows.
func TestCreateSandboxExposesPorts(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{Ports: []agents.SandboxPort{
		{Port: 3000, Visibility: agents.SandboxPortPrivate}, {Port: 8080, Visibility: agents.SandboxPortPublic}}})
	ports, err := s.SandboxPorts(ctx, account, sb.ID)
	if err != nil || len(ports) != 2 {
		t.Fatalf("ports = %+v, %v", ports, err)
	}
	route, err := s.PreviewRoute(ctx, sb.ID)
	if err != nil || route.Ports[3000] != agents.SandboxPortPrivate || route.Ports[8080] != agents.SandboxPortPublic {
		t.Fatalf("route = %+v, %v", route, err)
	}
	if _, err := s.CreateSandbox(ctx, sandboxAdmission(agents.SandboxAdmission{Ports: []agents.SandboxPort{{Port: 3000, Visibility: "open"}}})); err == nil {
		t.Fatal("a session with an invalid port was created")
	}
	var refused *agents.LimitError
	_, err = s.CreateSandbox(ctx, sandboxAdmission(agents.SandboxAdmission{QueuedMax: 1, Ports: []agents.SandboxPort{{Port: 4000, Visibility: agents.SandboxPortPrivate}}}))
	if !errors.As(err, &refused) {
		t.Fatalf("a session past the queue cap = %v", err)
	}
	list, err := s.Sandboxes(ctx, account, agents.SandboxFilter{Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("sessions = %d, %v", len(list), err)
	}
}
