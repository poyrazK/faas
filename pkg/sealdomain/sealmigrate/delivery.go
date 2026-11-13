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
	"github.com/onebox-faas/faas/pkg/triggerconfig"
)

// deliveryBatch is small because an execution payload runs to 3 MiB.
const deliveryBatch = 32

// deliveryKinds are the delivery values the re-seal and the check walk.
var deliveryKinds = []string{
	state.DeliverySealedAppWebhook,
	state.DeliverySealedCommitConnection,
	state.DeliverySealedExecutionPayload,
	state.DeliverySealedTriggerConfig,
}

// DeliveryStore is the slice of *state.PgStore the delivery re-seal uses.
type DeliveryStore interface {
	DeliveryLister
	ResealDeliveryValue(ctx context.Context, v state.DeliverySealedValue, sealed []byte, kid string) (bool, error)
}

// DeliveryLister is the slice of *state.PgStore the delivery check reads.
type DeliveryLister interface {
	ListDeliverySealedValues(ctx context.Context, kind, afterID string, limit int) ([]state.DeliverySealedValue, error)
}

// MigrateDelivery re-seals every app webhook signing secret, commit-source
// connection, pending execution payload and trigger config credential to
// the delivery domain recipient alone (ADR 20261022111812, step 3), and
// files each payload under the domain's key id. legacy are the fleet and
// host identities that open values sealed before the domain. Run it only
// after every schedd, on both node roles, holds the domain identity: a
// re-sealed value no longer opens with the fleet identity.
func MigrateDelivery(ctx context.Context, store DeliveryStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: delivery migration needs the store, the domain identity and the legacy identities")
	}
	return walkDelivery(ctx, store, domain, legacy, store.ResealDeliveryValue)
}

// CheckDelivery counts, without writing, the delivery values that legacy
// identities still open or that are stored in plaintext (ADR 20261022111812,
// the contract step's precondition): schedd may drop the fleet and host
// identities only once Pending is zero.
func CheckDelivery(ctx context.Context, store DeliveryLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil {
		return MigrationReport{}, errors.New("sealmigrate: delivery check needs the store and the domain identity")
	}
	return walkDelivery(ctx, store, domain, legacy, nil)
}

// walkDelivery visits every listed delivery value. With reseal nil it only
// counts.
func walkDelivery(ctx context.Context, store DeliveryLister, domain *age.X25519Identity, legacy []*age.X25519Identity, reseal func(ctx context.Context, v state.DeliverySealedValue, sealed []byte, kid string) (bool, error)) (MigrationReport, error) {
	kid := domain.Recipient().String()
	report := MigrationReport{Domain: sealdomain.Delivery, Target: kid}
	for _, kind := range deliveryKinds {
		afterID := ""
		for {
			page, err := store.ListDeliverySealedValues(ctx, kind, afterID, deliveryBatch)
			if err != nil {
				return report, err
			}
			for _, v := range page {
				report.Scanned++
				afterID = v.ID
				sealed, outcome, err := resealDeliveryValue(v, domain, legacy)
				if err != nil {
					return report, fmt.Errorf("sealmigrate: reseal delivery %s %s: %w", v.Kind, v.ID, err)
				}
				if reseal == nil {
					report.checked(outcome)
					continue
				}
				replaced := false
				if outcome == resealWrite {
					if replaced, err = reseal(ctx, v, sealed, kid); err != nil {
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

// resealDeliveryValue re-seals one value. A trigger config is JSON whose
// credentials are sealed leaves; every other value is one envelope.
func resealDeliveryValue(v state.DeliverySealedValue, domain *age.X25519Identity, legacy []*age.X25519Identity) ([]byte, resealOutcome, error) {
	if v.Kind != state.DeliverySealedTriggerConfig {
		namespace, maxBytes := deliveryNamespace(v)
		return resealBlob(v.Sealed, namespace, maxBytes, domain, legacy)
	}
	sealed, status, err := triggerconfig.Reseal(api.TriggerKind(v.TriggerKind), v.Sealed, domain, legacy)
	switch {
	case err != nil:
		return nil, resealFailed, err
	case status == triggerconfig.ResealWrite:
		return sealed, resealWrite, nil
	case status == triggerconfig.ResealCurrent:
		return nil, resealCurrent, nil
	default:
		return nil, resealFailed, nil
	}
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
