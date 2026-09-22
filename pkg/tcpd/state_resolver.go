package tcpd

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// ListenerStoreResolver adapts the durable state resource to tcpd's route
// resolver. The store only returns enabled listeners for public-port lookups,
// so a disabled or deleted listener fails closed as no route.
type ListenerStoreResolver struct {
	Store state.TCPListenerStore
}

// Resolve implements RouteResolver.
func (r ListenerStoreResolver) Resolve(ctx context.Context, publicPort int) (Route, bool, error) {
	if r.Store == nil {
		return Route{}, false, errors.New("tcpd listener store resolver has no store")
	}
	listener, err := r.Store.TCPListenerByPublicPort(ctx, publicPort)
	if errors.Is(err, state.ErrNotFound) {
		return Route{}, false, nil
	}
	if err != nil {
		return Route{}, false, fmt.Errorf("resolve TCP listener on public port %d: %w", publicPort, err)
	}
	route := Route{
		PublicPort:   listener.PublicPort,
		AppID:        listener.AppID,
		AccountID:    listener.AccountID,
		ListenerName: listener.ListenerName,
		GuestPort:    listener.GuestPort,
		Protocol:     listener.Protocol,
	}
	if err := ValidateRoute(route); err != nil {
		return Route{}, false, err
	}
	return route, true, nil
}
