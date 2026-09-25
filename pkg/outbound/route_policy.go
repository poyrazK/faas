package outbound

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
)

// RoutePolicy is a customer's app-specific narrowing of an integration's route
// policy. The gateway intersects it with the live integration ceiling.
type RoutePolicy = routepolicy.Policy

func (i Integration) validateRoutePolicy() error {
	if i.ProviderAuthMode != ProviderAuthManaged {
		if len(i.AllowedMethods) != 0 || len(i.AllowedPathPrefixes) != 0 {
			return fmt.Errorf("%w: route permissions require managed provider authentication", ErrInvalidIntegration)
		}
		return nil
	}
	if path := i.Origin.EscapedPath(); path != "" && !routepolicy.CanonicalPath(path) {
		return fmt.Errorf("%w: managed origin path is not canonical", ErrInvalidIntegration)
	}
	if err := routepolicy.Validate(RoutePolicy{AllowedMethods: i.AllowedMethods, AllowedPathPrefixes: i.AllowedPathPrefixes}); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidIntegration, err)
	}
	return nil
}

// AllowsRequest checks the app-supplied suffix before the fixed origin path
// is prepended and before admission or provider credentials are used.
func (i Integration) AllowsRequest(method, escapedPath string) bool {
	if i.ProviderAuthMode != ProviderAuthManaged {
		return true
	}
	return (RoutePolicy{AllowedMethods: i.AllowedMethods, AllowedPathPrefixes: i.AllowedPathPrefixes}).AllowsRequest(method, escapedPath)
}

// AllowsAppRequest applies the integration ceiling and then any customer app
// narrowing. An explicit operator attachment retains the full ceiling.
func (i Integration) AllowsAppRequest(appID, method, escapedPath string) bool {
	if !i.AllowsApp(appID) || !i.AllowsRequest(method, escapedPath) {
		return false
	}
	if _, operatorAttached := i.OperatorAppIDs[appID]; operatorAttached {
		return true
	}
	if policy, narrowed := i.CustomerAppRoutes[appID]; narrowed {
		return policy.AllowsRequest(method, escapedPath)
	}
	return true
}

func ValidateBindingRoutePolicy(ceiling, requested RoutePolicy) error {
	if err := routepolicy.ValidateSubset(ceiling, requested); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidIntegration, err)
	}
	return nil
}

func hasMethodOverride(r *http.Request) bool {
	for _, name := range []string{"X-HTTP-Method-Override", "X-Method-Override", "X-HTTP-Method"} {
		if r.Header.Get(name) != "" {
			return true
		}
	}
	return r.URL.Query().Has("_method")
}
