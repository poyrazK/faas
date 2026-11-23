package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// workflowOutboundUnavailable reports why schedd cannot run workflow
// integration steps (ADR-489), or "" when it can. With secretd minting, this
// box holds no cluster key and may not read it; secretd refuses each mint
// itself when it has none. A local minter needs the active cluster key
// outboundd verifies against.
func workflowOutboundUnavailable(ctx context.Context, viaSecretd bool, minter sched.WorkflowOutboundMinter, loadKey func(context.Context) (state.ClusterSigningKey, error)) string {
	if minter == nil {
		return "no workflow identity minter is wired"
	}
	if viaSecretd {
		return ""
	}
	key, err := loadKey(ctx)
	if err != nil {
		return "the cluster signing key could not be loaded: " + err.Error()
	}
	if key.RetiredAt != nil {
		return "the cluster signing key is retired"
	}
	return ""
}
