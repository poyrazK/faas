package sealmigrate

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/state"
)

// deliveryBatch is small because an execution payload runs to 3 MiB.
const deliveryBatch = 32

// DeliveryStore is the slice of *state.PgStore the delivery re-seal uses.
type DeliveryStore interface {
	ListDeliverySealedValues(ctx context.Context, kind, afterID string, limit int) ([]state.DeliverySealedValue, error)
	ResealDeliveryValue(ctx context.Context, v state.DeliverySealedValue, sealed []byte, kid string) (bool, error)
}

// MigrateDelivery re-seals every app webhook signing secret, commit-source
// connection and pending execution payload to the delivery domain
// recipient alone (ADR 20261022111812, step 3), and files each payload
// under the domain's key id. legacy are the fleet and host identities that
// open values sealed before the domain. Run it only after every schedd, on
// both node roles, holds the domain identity: a re-sealed value no longer
// opens with the fleet identity.
func MigrateDelivery(ctx context.Context, store DeliveryStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: delivery migration needs the store, the domain identity and the legacy identities")
	}
	kid := domain.Recipient().String()
	report := MigrationReport{Domain: sealdomain.Delivery, Target: kid}
	for _, kind := range []string{state.DeliverySealedAppWebhook, state.DeliverySealedCommitConnection, state.DeliverySealedExecutionPayload} {
		afterID := ""
		for {
			page, err := store.ListDeliverySealedValues(ctx, kind, afterID, deliveryBatch)
			if err != nil {
				return report, err
			}
			for _, v := range page {
				report.Scanned++
				afterID = v.ID
				namespace, maxBytes := deliveryNamespace(v)
				sealed, outcome, err := resealBlob(v.Sealed, namespace, maxBytes, domain, legacy)
				if err != nil {
					return report, fmt.Errorf("sealmigrate: reseal delivery %s %s: %w", v.Kind, v.ID, err)
				}
				replaced := false
				if outcome == resealWrite {
					if replaced, err = store.ResealDeliveryValue(ctx, v, sealed, kid); err != nil {
						return report, err
					}
				}
				report.count(outcome, replaced)
			}
			if len(page) < deliveryBatch {
				break
			}
		}
	}
	return report, nil
}

func deliveryNamespace(v state.DeliverySealedValue) (string, int) {
	switch v.Kind {
	case state.DeliverySealedCommitConnection:
		return commit.ConnectionNamespace(v.ID), commit.CredentialMaxBytes
	case state.DeliverySealedExecutionPayload:
		return executionpayload.Namespace, api.ExecutionSealedPayloadMaxBytes
	default:
		return api.AppWebhookSecretNamespace, api.AppWebhookSecretMaxBytes
	}
}
