// adr: 167
package objectstorage

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func storageRequestHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if name, port, err := net.SplitHostPort(host); err == nil && port != "" {
		host = name
	} else {
		host = strings.Trim(host, "[]")
	}
	return strings.TrimSuffix(host, ".")
}

// storageTenantBinding mirrors ingress precedence. A verified suspended
// platform tenant owns its hostname even when an older custom claim exists.
func storageTenantBinding(ctx context.Context, store PublicReadStore, host string) (state.PlatformTenantHostBinding, error) {
	reader, ok := store.(state.PlatformTenantHostBindingReader)
	if !ok {
		return state.PlatformTenantHostBinding{}, errors.New("object storage: tenant hostname reader unavailable")
	}
	binding, err := reader.PlatformTenantHostBinding(ctx, host)
	if err != nil {
		return binding, err
	}
	if binding.Verified && binding.Suspended {
		return binding, state.ErrPlatformTenantSuspended
	}
	if !binding.Active || !binding.Verified {
		return binding, state.ErrNotFound
	}
	return binding, nil
}

func storageTenantApp(ctx context.Context, store PublicReadStore, binding state.PlatformTenantHostBinding) (state.App, error) {
	app, err := store.AppByID(ctx, binding.AppID)
	if err != nil {
		return state.App{}, err
	}
	if app.AccountID != binding.AccountID {
		return state.App{}, state.ErrNotFound
	}
	return app, nil
}

func storageDomainApp(ctx context.Context, store PublicReadStore, host string, tenantSurfaces bool) (state.App, error) {
	if tenantSurfaces {
		binding, err := storageTenantBinding(ctx, store, host)
		if err == nil {
			return storageTenantApp(ctx, store, binding)
		}
		if !errors.Is(err, state.ErrNotFound) {
			return state.App{}, err
		}
	}
	return customDomainApp(ctx, store, host)
}

// customDomainApp uses the same exact-before-wildcard reservation rules as
// application ingress. A live pending claim blocks the broader wildcard;
// an expired pending claim does not. Reads and uploads must agree on the app.
func customDomainApp(ctx context.Context, store PublicReadStore, host string) (state.App, error) {
	domain, err := store.DomainByName(ctx, host)
	if err == nil && domain.ClaimLive(time.Now()) {
		if !domain.Verified() {
			return state.App{}, state.ErrNotFound
		}
		return store.AppByID(ctx, domain.AppID)
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return state.App{}, err
	}
	wildcards, ok := store.(state.CustomDomainWildcardStore)
	if !ok {
		return state.App{}, state.ErrNotFound
	}
	domain, err = wildcards.WildcardDomainForHost(ctx, host)
	if err != nil {
		return state.App{}, err
	}
	if !domain.Verified() {
		return state.App{}, state.ErrNotFound
	}
	app, err := store.AppByID(ctx, domain.AppID)
	if err != nil {
		return state.App{}, err
	}
	owners, ok := store.(state.AppOwnerReader)
	if !ok {
		return state.App{}, state.ErrNotFound
	}
	owner, err := state.AppResourceAccount(ctx, owners, app)
	if err != nil {
		return state.App{}, err
	}
	if !owner.Plan.WildcardDomainsAllowed() {
		return state.App{}, state.ErrNotFound
	}
	return app, nil
}
