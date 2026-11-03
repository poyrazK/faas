package fcvm

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// withEventPublishVerdict tells an app VM's guest-init that this vmmd answers
// in-guest event publishes with a verdict (ADR-20261031102000). Builders run
// no publish endpoint, so they keep their command line.
func withEventPublishVerdict(l Lease, cfg VMConfig) VMConfig {
	if l.IsBuilder || strings.Contains(cfg.BootSource.BootArgs, api.EventPublishVerdictBootArg) {
		return cfg
	}
	cfg.BootSource.BootArgs = strings.TrimSpace(cfg.BootSource.BootArgs) + " " + api.EventPublishVerdictBootArg
	return cfg
}
