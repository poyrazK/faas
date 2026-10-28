// adr: 570
package state

import "github.com/onebox-faas/faas/pkg/hostidentity"

type storeOptions struct {
	trafficAppsDomain    string
	trafficScopedBinding bool
}

// StoreOption configures immutable store wiring before its first use.
type StoreOption func(*storeOptions)

// WithTrafficAppsDomain supplies the same apps_domain as the public router.
// Empty disables primary/alias app URL analysis; revision URLs remain separate.
func WithTrafficAppsDomain(domain string) StoreOption {
	return func(options *storeOptions) { options.trafficAppsDomain = domain }
}

// WithTrafficScopedBindingGuard makes binding writes lock and analyse only
// the hostnames they declare instead of the whole fleet (ADR-20261027163626).
// Writers with and without it exclude each other through the root lock.
func WithTrafficScopedBindingGuard(on bool) StoreOption {
	return func(options *storeOptions) { options.trafficScopedBinding = on }
}

func configuredTrafficScopedBinding(options []StoreOption) bool {
	var config storeOptions
	for _, option := range options {
		option(&config)
	}
	return config.trafficScopedBinding
}

func configuredTrafficAppsSuffix(options []StoreOption) string {
	config := storeOptions{trafficAppsDomain: hostidentity.DefaultAppsDomain}
	for _, option := range options {
		option(&config)
	}
	return hostidentity.AppsSuffix(config.trafficAppsDomain)
}
