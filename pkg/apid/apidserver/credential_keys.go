package apidserver

import (
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// installCredentialPolicy loads the account family's credential policy
// (ADR 20261029202403; credhash.LoadAccountPolicy names the variables) and
// installs it for this process's stores.
func installCredentialPolicy(getenv func(string) string, log *slog.Logger) error {
	p, err := credhash.LoadAccountPolicy(getenv)
	if err != nil {
		return fmt.Errorf("apid: %w", err)
	}
	credhash.SetAccount(p)
	current := 0
	if p.Ring != nil {
		current = int(p.Ring.Current())
	}
	log.Info("credential hashes", "keyed_writes", p.KeyedWrites, "plain_lookup", p.PlainLookup, "current_key", current)
	return nil
}
