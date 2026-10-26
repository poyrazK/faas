// adr: 20261025111015

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// A restore brings s3_enabled back as it was at the restore point. The hold
// writes it off as applied, in the database alone, so every gateway's
// watcher turns S3 and public reads off before the catalog is reconciled.
func TestObjectStorageHoldTurnsS3OffAsApplied(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemStore()
	on := json.RawMessage("true")
	restored, err := st.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: runtimeconfig.KeyS3, DesiredValue: on, Reason: "before the restore point"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkRuntimeConfigApplied(ctx, runtimeconfig.KeyS3, state.RuntimeConfigScopeGlobal, "", restored.Version, on, ""); err != nil {
		t.Fatal(err)
	}
	version, err := holdObjectStorage(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.GetRuntimeConfig(ctx, runtimeconfig.KeyS3, state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	if row.Version != version || version <= restored.Version || row.Status != state.RuntimeConfigApplied ||
		string(row.EffectiveValue) != "false" || row.Reason != objectStorageHoldReason {
		t.Fatalf("s3_enabled after the hold = %+v", row)
	}
	// Holding again is harmless.
	if again, err := holdObjectStorage(ctx, st); err != nil || again <= version {
		t.Fatalf("second hold = %d, %v", again, err)
	}
}
