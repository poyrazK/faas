// Package tcpd contains the public raw-TCP listener and routing seam.
//
// The package deliberately does not know how applications are stored or
// woken. It resolves a public listener to a stable app/listener identity and
// delegates instance selection to the caller.
package tcpd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/gateway"
)

var (
	// ErrNoRoute indicates that no listener owns a public TCP port.
	ErrNoRoute = errors.New("no TCP route")
	// ErrInvalidRoute indicates that a route violates the tcpd contract.
	ErrInvalidRoute = errors.New("invalid TCP route")
)

// Route maps one public TCP listener to an application listener.
//
// PublicPort is the stable edge-facing endpoint. GuestPort is the port inside
// the workload network namespace; it is copied onto the gateway target before
// forwarding so the target cannot accidentally select the HTTP port.
type Route struct {
	PublicPort int
	AppID      string
	// AccountID scopes connection quotas without exposing account metadata to
	// the forwarding transport. It is optional for in-memory route tables and
	// falls back to AppID when absent.
	AccountID    string
	ListenerName string
	GuestPort    int
	Protocol     string
}

// ValidateRoute enforces the route contract used by the listener and route
// table. Protocol is intentionally explicit even though this package only
// serves TCP; it prevents a UDP lease from being attached to a TCP socket.
func ValidateRoute(route Route) error {
	if route.PublicPort < 1 || route.PublicPort > 65535 {
		return fmt.Errorf("%w: public port %d is outside 1..65535", ErrInvalidRoute, route.PublicPort)
	}
	if strings.TrimSpace(route.AppID) == "" {
		return fmt.Errorf("%w: app ID is empty", ErrInvalidRoute)
	}
	if strings.TrimSpace(route.ListenerName) == "" {
		return fmt.Errorf("%w: listener name is empty", ErrInvalidRoute)
	}
	if route.GuestPort < 1 || route.GuestPort > 65535 {
		return fmt.Errorf("%w: guest port %d is outside 1..65535", ErrInvalidRoute, route.GuestPort)
	}
	if !strings.EqualFold(strings.TrimSpace(route.Protocol), "tcp") {
		return fmt.Errorf("%w: protocol %q is not tcp", ErrInvalidRoute, route.Protocol)
	}
	return nil
}

// RouteResolver resolves the public port from an accepted TCP connection.
type RouteResolver interface {
	Resolve(ctx context.Context, publicPort int) (Route, bool, error)
}

// TargetResolver selects a live instance for a route. tcpd owns the route's
// app identity and guest port; the returned target supplies the node and
// instance IDs used by gateway.TCPForwarder.
type TargetResolver interface {
	ResolveTarget(ctx context.Context, route Route) (gateway.Target, error)
}

// RouteTable is a concurrency-safe in-memory route index. Production wiring
// can back the same RouteResolver contract with Postgres; this implementation
// is useful for listener lifecycle code, tests, and staged rollout.
type RouteTable struct {
	mu       sync.RWMutex
	byPort   map[int]Route
	byTarget map[routeTarget]int
}

type routeTarget struct {
	appID        string
	listenerName string
}

// NewRouteTable returns an empty route table.
func NewRouteTable() *RouteTable {
	return &RouteTable{
		byPort:   make(map[int]Route),
		byTarget: make(map[routeTarget]int),
	}
}

// Upsert adds or replaces a route. A listener identity can move to a new
// public port, but a public port cannot be shared by two identities.
func (t *RouteTable) Upsert(route Route) error {
	if err := ValidateRoute(route); err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("%w: nil route table", ErrInvalidRoute)
	}
	target := routeTarget{appID: route.AppID, listenerName: route.ListenerName}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byPort == nil {
		t.byPort = make(map[int]Route)
	}
	if t.byTarget == nil {
		t.byTarget = make(map[routeTarget]int)
	}
	if existing, ok := t.byPort[route.PublicPort]; ok {
		existingTarget := routeTarget{appID: existing.AppID, listenerName: existing.ListenerName}
		if existingTarget != target {
			return fmt.Errorf("%w: public port %d already belongs to app %q listener %q", ErrInvalidRoute, route.PublicPort, existing.AppID, existing.ListenerName)
		}
	}
	if oldPort, ok := t.byTarget[target]; ok && oldPort != route.PublicPort {
		delete(t.byPort, oldPort)
	}
	t.byPort[route.PublicPort] = route
	t.byTarget[target] = route.PublicPort
	return nil
}

// Resolve implements RouteResolver.
func (t *RouteTable) Resolve(ctx context.Context, publicPort int) (Route, bool, error) {
	if err := ctx.Err(); err != nil {
		return Route{}, false, err
	}
	if t == nil {
		return Route{}, false, fmt.Errorf("%w: nil route table", ErrInvalidRoute)
	}
	t.mu.RLock()
	route, ok := t.byPort[publicPort]
	t.mu.RUnlock()
	return route, ok, nil
}

// Delete removes the route for a public port and reports whether one existed.
func (t *RouteTable) Delete(publicPort int) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	route, ok := t.byPort[publicPort]
	if !ok {
		return false
	}
	delete(t.byPort, publicPort)
	delete(t.byTarget, routeTarget{appID: route.AppID, listenerName: route.ListenerName})
	return true
}
