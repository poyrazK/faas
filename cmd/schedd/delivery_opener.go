package main

import (
	"github.com/onebox-faas/faas/pkg/sched"
)

// triggerConfigOpenerFor returns secretd's OpenTriggerConfig when this
// schedd opens delivery values through it (ADR-20261110210000 slice 3g), or
// nil to keep opening them locally.
func triggerConfigOpenerFor(rpc sched.SecretGrantRPC, viaSecretd bool) sched.TriggerConfigOpener {
	if !viaSecretd || rpc == nil {
		return nil
	}
	client, ok := rpc.(secretdGrants)
	if !ok {
		return nil
	}
	return client.OpenTriggerConfig
}
