// adr: 570
package state

import "github.com/onebox-faas/faas/pkg/hostidentity"

type storeOptions struct{ trafficAppsDomain string }

// StoreOption configures immutable store wiring before its first use.
type StoreOption func(*storeOptions)

// WithTrafficAppsDomain supplies the same apps_domain as the public router.
// Empty disables primary/alias app URL analysis; revision URLs remain separate.
func WithTrafficAppsDomain(domain string) StoreOption {
	return func(options *storeOptions) { options.trafficAppsDomain = domain }
}

func configuredTrafficAppsSuffix(options []StoreOption) string {
	config := storeOptions{trafficAppsDomain: hostidentity.DefaultAppsDomain}
	for _, option := range options {
		option(&config)
	}
	return hostidentity.AppsSuffix(config.trafficAppsDomain)
}
