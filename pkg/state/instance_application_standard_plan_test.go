package state

// adr: 431/422 — standards compose with ordinary lifecycle and capacity checks.

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemInstanceApplicationStandardUnmanagedPlanCompatibility(t *testing.T) {
	standardUnmanagedPlanCompatibility(t, NewMemStore())
}

func TestMemInstanceApplicationStandardManagedPlanStaysStrict(t *testing.T) {
	standardManagedPlanStaysStrict(t, NewMemStore())
}

func standardUnmanagedPlanCompatibility(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, false)
	if err := s.UpdateAccountPlan(t.Context(), f.app.AccountID, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(StateWarm), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil || before.Managed {
		t.Fatalf("unmanaged fixture acquired authority: %+v %v", before, err)
	}
	fresh, err := s.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(StateWaking), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountPlan(t.Context(), f.app.AccountID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	account, err := s.AccountByID(t.Context(), f.app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckInstanceApplicationStandardAdmission(t.Context(), s, fresh.ID, f.app, account, f.dep); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("new unmanaged boot accepted stale plan inputs: %v", err)
	}
	if err := s.UpdateInstanceState(t.Context(), fresh.ID, string(StateRunning)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("plan compatibility authorized first runtime publication: %v", err)
	}
	if err := CheckInstanceApplicationStandardAdmission(t.Context(), s, ins.ID, f.app, account, f.dep); err != nil {
		t.Fatalf("current unmanaged plan refused scheduler read: %v", err)
	}
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateRunning)); err != nil {
		t.Fatalf("unmanaged historical guest could not promote: %v", err)
	}
	after, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil || before.InputHash != after.InputHash || before.NativeInputHash != after.NativeInputHash || !before.CapturedAt.Equal(after.CapturedAt) || after.Managed {
		t.Fatalf("compatibility rewrote admission history: %+v %v", after, err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), f.dep.ID, "/changed-plan.ext4", "layers/changed-plan.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateWarm)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("plan compatibility ignored a changed artifact: %v", err)
	}
}

func standardManagedPlanStaysStrict(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f, ins, _ := promotionTestParent(t, s)
	before, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil || !before.Managed {
		t.Fatalf("missing managed capture: %+v %v", before, err)
	}
	if err := s.UpdateAccountPlan(t.Context(), f.app.AccountID, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardWarmParent(t.Context(), ins.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("plan compatibility weakened managed native inputs: %v", err)
	}
	actual, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || actual.State != string(StateWarm) || actual.Netns != ins.Netns {
		t.Fatalf("refusal changed paused runtime: %+v %v", actual, err)
	}
	account, err := s.AccountByID(t.Context(), f.app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckInstanceApplicationStandardAdmission(t.Context(), s, ins.ID, f.app, account, f.dep); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("managed scheduler inputs lost plan fence: %v", err)
	}
}

func TestApplicationStandardRuntimeInputsMatchIsNarrow(t *testing.T) {
	before := []byte(`{"adoptions":[],"materialized_fields":[],"account_plan":"scale","bytes":9007199254740993}`)
	for _, tc := range []struct {
		name, current string
		want          bool
	}{
		{"only unmanaged plan", `{"adoptions":[],"materialized_fields":[],"account_plan":"pro","bytes":9007199254740993}`, true},
		{"integer precision", `{"adoptions":[],"materialized_fields":[],"account_plan":"pro","bytes":9007199254740992}`, false},
		{"new adoption", `{"adoptions":[{"version":1}],"materialized_fields":[],"account_plan":"pro","bytes":9007199254740993}`, false},
		{"retained managed field", `{"adoptions":[],"materialized_fields":["require_signed"],"account_plan":"pro","bytes":9007199254740993}`, false},
		{"missing plan", `{"adoptions":[],"materialized_fields":[],"bytes":9007199254740993}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := standardRuntimeInputsMatch(before, []byte(tc.current))
			if err != nil || got != tc.want {
				t.Fatalf("match=%v want=%v err=%v", got, tc.want, err)
			}
		})
	}
}
