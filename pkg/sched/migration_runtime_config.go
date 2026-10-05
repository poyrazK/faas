package sched

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

type migrationRuntimeInputs struct {
	ExpectedWakeID string
	WakeID         string
	Cold           state.RuntimeConfigInputs
	Restored       *state.RuntimeConfigInputs
}

// A restored process retains captured inputs; a cold fallback used the flat
// spec's freshly prepared inputs. Missing/unknown method or wake identity is
// deliberately unacknowledged, including replies from older vmmd peers.
func (h *MigrationHarness) commitMigrationRuntime(ctx context.Context, id, from, to, leaseToken string, spec AppSpec, adopted LiveMigrationAdopt) error {
	publisher, ok := h.store.(state.RuntimeConfigMigrationStore)
	if !ok || spec.migrationRuntime == nil {
		return h.store.MigrateInstanceOwner(ctx, id, from, to, leaseToken)
	}
	prepared := spec.migrationRuntime
	var inputs *state.RuntimeConfigInputs
	if adopted.WakeID == prepared.WakeID {
		switch adopted.Method {
		case vmmdpb.WakeMethod_WAKE_RESTORE:
			inputs = prepared.Restored
		case vmmdpb.WakeMethod_WAKE_COLD_BOOT:
			inputs = &prepared.Cold
		}
	}
	return publisher.MigrateInstanceOwnerWithRuntimeConfig(ctx, id, from, to, leaseToken, state.RuntimeConfigMigration{
		ExpectedWakeID: prepared.ExpectedWakeID, WakeID: prepared.WakeID, Inputs: inputs,
		Netns: adopted.Netns, HostIP: adopted.HostIP, GuestUID: adopted.GuestUID,
	})
}
