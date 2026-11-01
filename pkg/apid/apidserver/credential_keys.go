package apidserver

import (
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// apidCredentialFamilies are the credential families apid issues or checks
// (ADR 20261029202403 and its amendment 1): every family but uploads.
// gatewayd-internal and realtimed ask apid to match the families they
// check.
var apidCredentialFamilies = []credhash.Family{
	credhash.FamilyAccount,
	credhash.FamilyScheduler,
	credhash.FamilyOutbound,
	credhash.FamilyAttach,
	credhash.FamilyDevBridge,
	credhash.FamilyDataAPI,
}

// installCredentialPolicy loads the policy of each family apid holds
// (credhash.EnvNames names the variables) and installs it for this
// process's stores.
func installCredentialPolicy(getenv func(string) string, log *slog.Logger) error {
	if err := credhash.Install(getenv, apidCredentialFamilies...); err != nil {
		return fmt.Errorf("apid: %w", err)
	}
	for _, family := range apidCredentialFamilies {
		p := credhash.For(family)
		current := 0
		if p.Ring != nil {
			current = int(p.Ring.Current())
		}
		log.Info("credential hashes", "family", family, "keyed_writes", p.KeyedWrites, "plain_lookup", p.PlainLookup, "current_key", current)
	}
	return nil
}
