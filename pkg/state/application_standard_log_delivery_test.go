package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardLogDeliveryTestStore interface {
	standardLocalIntentTestStore
	ApplicationStandardLogDeliveryStore
	ListEnabledAppLogDrains(context.Context) ([]AppLogDrain, error)
}

func TestMemApplicationStandardLogDelivery(t *testing.T) {
	standardLogDeliveryLifecycle(t, NewMemStore())
}

func standardLogDeliveryInstance(t *testing.T, s standardLogDeliveryTestStore, f standardLocalIntentFixture) Instance {
	t.Helper()
	ctx := t.Context()
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: f.app.ID, Kind: DeploymentKindImage, Status: DeploySuperseded})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateComputeNode(ctx, ComputeNode{Name: "logging-source-" + uuid.NewString(), TargetURL: "unix:///tmp/standards-log-source", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(ctx, f.app.ID, dep.ID, "stopped", 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return ins
}

func standardLogDeliveryDrain(t *testing.T, s standardLogDeliveryTestStore, f standardLocalIntentFixture) AppLogDrain {
	t.Helper()
	rows, err := s.ListEnabledAppLogDrains(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range rows {
		if sameStandardUUID(d.AppID, f.app.ID) && d.StandardBinding != nil && d.StandardBinding.ResourceID == f.company.ID {
			return d
		}
	}
	t.Fatalf("missing standard binding for app %s", f.app.ID)
	return AppLogDrain{}
}

func standardLogDeliveryLifecycle(t *testing.T, s standardLogDeliveryTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	ins := standardLogDeliveryInstance(t, s, f)
	d := standardLogDeliveryDrain(t, s, f)
	if d.StandardBinding.DrainConfigHash != ApplicationStandardLogDrainConfigHash(d) {
		t.Fatal("SQL/Go sender hash differs")
	}
	standardLogDeliveryRefusals(t, s, d, ins.ID)
	got, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 7)
	if err != nil || got.DesiredRevision != f.enrollment.DesiredRevision || got.Sequence != 7 || got.ObservedAt.IsZero() {
		t.Fatalf("delivery: %+v %v", got, err)
	}
	standardLogDeliveryInspect(t, s, f, d)
	r := ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: mustStandardLocalJSON(appstandards.Settings{appstandards.EgressExtraPorts: json.RawMessage(`[8443]`)}), AdditionalLogDestinations: []string{f.extra.ID}}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 8); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("pending receipt accepted: %v", err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, d, ins.ID, 8); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("old revision accepted: %v", err)
	}
	current := standardLogDeliveryDrain(t, s, f)
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, current, ins.ID, 8); err != nil {
		t.Fatal(err)
	}
	standardLogDeliveryInspect(t, s, f, current)
	standardLocalIntentRotateCompany(ctx, t, s, &f)
	if _, err := s.RecordApplicationStandardLogDelivery(ctx, current, ins.ID, 9); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("removed destination accepted: %v", err)
	}
	rows, err := s.ListApplicationStandardLogDeliveries(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleted physical drain retained receipts: %+v %v", rows, err)
	}
}

func standardLogDeliveryInspect(t *testing.T, s standardLogDeliveryTestStore, f standardLocalIntentFixture, d AppLogDrain) {
	t.Helper()
	ctx := t.Context()
	rows, err := s.ListApplicationStandardLogDeliveries(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != 1 || rows[0].ApplicationStandardLogDrainBinding != *d.StandardBinding {
		t.Fatalf("receipt list: %+v %v", rows, err)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{d.TargetURL, string(d.AuthHeaderSealed), "sealed-"} {
		if secret != "" && strings.Contains(string(raw), secret) {
			t.Fatal("receipt exposed configuration")
		}
	}
	encoded, err := json.Marshal(d)
	if err != nil || strings.Contains(string(encoded), "standard_binding") || strings.Contains(string(encoded), d.StandardBinding.EffectiveHash) {
		t.Fatal("private binding serialized")
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || e.ObservedRevision != 0 || e.State != "persisted" || e.DesiredRevision != f.enrollment.DesiredRevision {
		t.Fatalf("receipt advanced enrollment: %+v %v", e, err)
	}
	if _, err := s.ListApplicationStandardLogDeliveries(ctx, uuid.NewString(), f.app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org read: %v", err)
	}
}

func standardLogDeliveryRefusals(t *testing.T, s standardLogDeliveryTestStore, d AppLogDrain, source string) {
	t.Helper()
	tests := []struct {
		name   string
		mutate func(*AppLogDrain)
		source string
		seq    uint64
		want   error
	}{
		{"no binding", func(d *AppLogDrain) { d.StandardBinding = nil }, source, 1, ErrInvalidArgument},
		{"zero sequence", nil, source, 0, ErrInvalidArgument},
		{"oversize sequence", nil, source, uint64(api.ApplicationStandardMaxLogSequence) + 1, ErrInvalidArgument},
		{"unknown source", nil, uuid.NewString(), 1, ErrApplicationStandardLogDeliveryStale},
		{"changed URL", func(d *AppLogDrain) { d.TargetURL = "https://wrong.example.com" }, source, 1, ErrInvalidArgument},
		{"changed ciphertext", func(d *AppLogDrain) { d.AuthHeaderSealed = []byte("another credential") }, source, 1, ErrInvalidArgument},
		{"forged tuple", func(d *AppLogDrain) {
			d.TargetURL = "https://wrong.example.com"
			d.StandardBinding.DrainConfigHash = ApplicationStandardLogDrainConfigHash(*d)
		}, source, 1, ErrApplicationStandardLogDeliveryStale},
		{"changed scope", func(d *AppLogDrain) { d.StandardBinding.OrgID = uuid.NewString() }, source, 1, ErrApplicationStandardLogDeliveryStale},
		{"changed resource", func(d *AppLogDrain) { d.StandardBinding.ResourceID = uuid.NewString() }, source, 1, ErrApplicationStandardLogDeliveryStale},
		{"changed resource hash", func(d *AppLogDrain) { d.StandardBinding.ResourceConfigHash = strings.Repeat("a", 64) }, source, 1, ErrApplicationStandardLogDeliveryStale},
		{"changed effective hash", func(d *AppLogDrain) { d.StandardBinding.EffectiveHash = strings.Repeat("b", 64) }, source, 1, ErrApplicationStandardLogDeliveryStale},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			copy := cloneAppLogDrain(d)
			if tc.mutate != nil {
				tc.mutate(&copy)
			}
			_, err := s.RecordApplicationStandardLogDelivery(t.Context(), copy, tc.source, tc.seq)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
