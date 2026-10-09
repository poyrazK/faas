package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimefence"
)

type RuntimeUpgradeExternalFenceAuthority struct {
	ID        string
	PublicKey []byte
	CreatedAt time.Time
	RevokedAt *time.Time
}

type RuntimeUpgradeExternalFenceReview struct {
	ID, WithdrawalID, AuthorityID   string
	GatewayRevision, PublicRevision string
	MachineID, BootID, ResourceID   string
	ScopeSHA256                     string
}

type RuntimeUpgradeExternalFenceReceipt struct {
	WithdrawalID, IntentID, ReceiptID string
	EnvelopeSHA256                    string
	Envelope                          []byte
	EnforcedAt, IssuedAt, ObservedAt  time.Time
}

// Private administrative reviews and signed evidence. No production adapter,
// customer route, daemon wiring or host/network mutation is provided (ADR-708).
type RuntimeUpgradeExternalFenceStore interface {
	ReviewRuntimeUpgradeExternalFenceAuthority(context.Context, string, []byte) (RuntimeUpgradeExternalFenceAuthority, error)
	RevokeRuntimeUpgradeExternalFenceAuthority(context.Context, string) error
	ReviewRuntimeUpgradeExternalFenceIntent(context.Context, RuntimeUpgradeExternalFenceReview) (runtimefence.Intent, error)
	RecordRuntimeUpgradeExternalFenceReceipt(context.Context, string, []byte) (RuntimeUpgradeExternalFenceReceipt, error)
}
