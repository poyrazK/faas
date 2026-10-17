package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceTargetEvidenceIsolationMem(t *testing.T) {
	serviceTargetEvidenceIsolation(t, state.NewMemStore())
}
func TestServiceTargetEvidenceIsolationPG(t *testing.T) {
	store, _ := pgStore(t)
	serviceTargetEvidenceIsolation(t, store)
}

func serviceTargetEvidenceIsolation(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, app, _, candidate := bindingPromotionFixture(t, store)
	target1, target2 := uuid.NewString(), uuid.NewString()
	for i, target := range []string{"", target1, target2} {
		command := []string{api.AppTaskServiceBindingProbeCommand, "billing"}
		if target != "" {
			command = []string{api.AppTaskServiceBindingTargetProbeCommand, "billing", target}
		}
		_, err := store.(state.AppTaskStore).CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: candidate.ID, Kind: state.AppTaskKindManual, Command: command, TimeoutSeconds: 30, MaxOutputBytes: 4096, CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond), RequireLiveDeployment: true, BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: "billing", Revision: strings.Repeat("a", 64), TargetDeploymentID: target}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"", target1, target2, uuid.NewString()} {
		selection := state.BindingVerificationSelection{DeploymentID: candidate.ID}
		if target != "" {
			selection.ServiceTargets = map[string]string{"billing": target}
		}
		rows, err := store.(state.BindingVerificationStore).ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypeService}, selection)
		if err != nil {
			t.Fatal(err)
		}
		if target != "" && target != target1 && target != target2 {
			if len(rows) != 0 {
				t.Fatal("cross-target fallback")
			}
			continue
		}
		if len(rows) != 1 || rows[0].Pin.TargetDeploymentID != target {
			t.Fatalf("target=%s rows=%+v", target, rows)
		}
	}
}
