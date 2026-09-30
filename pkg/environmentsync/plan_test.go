package environmentsync

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPlanOwnershipAdoptionAndDrift(t *testing.T) {
	desired, err := Compile(definition())
	if err != nil {
		t.Fatal(err)
	}
	presence, field := desired.Fields[0], desired.Fields[1]
	changed := field
	changed.Value = json.RawMessage(`"console-change"`)
	opts := PlanOptions{Manager: "git-source", Revision: "approved-sha", Generation: 1}
	for _, tc := range []struct {
		name   string
		fields []Field
		owners []Ownership
		adopt  bool
		action string
		block  bool
		drift  bool
	}{
		{"new field", nil, nil, false, "create", false, true},
		{"unmanaged equal", []Field{field}, nil, false, "conflict", true, true},
		{"reviewed adoption", []Field{changed}, nil, true, "adopt", false, true},
		{"owned equal", []Field{field}, []Ownership{{Field: field, Manager: "git-source"}}, false, "keep", false, false},
		{"owned drift", []Field{changed}, []Ownership{{Field: field, Manager: "git-source"}}, false, "update", false, true},
		{"owned missing", nil, []Ownership{{Field: field, Manager: "git-source"}}, false, "update", false, true},
		{"terraform conflict", []Field{field}, []Ownership{{Field: field, Manager: "terraform"}}, true, "conflict", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts.Adopt = tc.adopt
			observedFields := append([]Field{presence}, tc.fields...)
			owners := append([]Ownership{{Field: presence, Manager: opts.Manager}}, tc.owners...)
			plan, err := BuildPlan(desired, ObservedState{Version: 1, Fields: observedFields}, owners, opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Changes) != 2 || plan.Changes[1].Action != tc.action || plan.CanApply() == tc.block || plan.HasDrift() != tc.drift {
				t.Fatalf("unexpected plan %+v", plan)
			}
		})
	}
}

func TestPlanNewCommitReconcilesCodeWithUnchangedManifest(t *testing.T) {
	d := definition()
	w := d.Workloads["api"]
	w.Source = &api.EnvironmentWorkloadSource{Kind: "source", Directory: "api"}
	d.Workloads["api"] = w
	desired, err := Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	oldSHA, newSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	oldValue, _ := json.Marshal(oldSHA)
	code := Field{Resource: "workload/api", Path: "source_revision", Value: oldValue}
	observed := ObservedState{Fields: append(slices.Clone(desired.Fields), code)}
	owners := []Ownership{}
	for _, field := range observed.Fields {
		owners = append(owners, Ownership{Field: field, Manager: "git"})
	}
	opts := PlanOptions{Manager: "git", Revision: "revision", CommitSHA: oldSHA, Generation: 1}
	current, err := BuildPlan(desired, observed, owners, opts)
	if err != nil || current.HasDrift() {
		t.Fatalf("current approved code not converged: %+v %v", current, err)
	}
	opts.CommitSHA, opts.Generation = newSHA, 2
	updated, err := BuildPlan(desired, observed, owners, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.HasDrift() || updated.Hash == current.Hash {
		t.Fatal("new code commit treated as configuration no-op")
	}
	for _, change := range updated.Changes {
		if change.Path == "source_revision" && change.Action == "update" {
			return
		}
	}
	t.Fatal("source revision was not scheduled for update")
}

func TestPlanPrunesOnlyPreviouslyOwnedFields(t *testing.T) {
	desired, _ := Compile(definition())
	managed := desired.Fields[0]
	removed := Field{Resource: "workload/api", Path: "variables/OLD", Value: json.RawMessage(`"old"`)}
	unmanaged := Field{Resource: "workload/api", Path: "variables/MANUAL", Value: json.RawMessage(`"manual"`)}
	foreign := Field{Resource: "workload/api", Path: "variables/TF", Value: json.RawMessage(`"tf"`)}
	owners := []Ownership{{Field: managed, Manager: "git"}, {Field: desired.Fields[1], Manager: "git"}, {Field: removed, Manager: "git"}, {Field: foreign, Manager: "terraform"}}
	observed := ObservedState{Fields: []Field{managed, desired.Fields[1], removed, unmanaged, foreign}}
	for _, prune := range []bool{false, true} {
		plan, err := BuildPlan(desired, observed, owners, PlanOptions{Manager: "git", Revision: "sha", Generation: 1, Prune: prune})
		if err != nil {
			t.Fatal(err)
		}
		actions := map[string]string{}
		for _, change := range plan.Changes {
			actions[change.Path] = change.Action
		}
		want := "remove_candidate"
		if prune {
			want = "remove"
		}
		if actions["variables/OLD"] != want || actions["variables/MANUAL"] != "retain_unmanaged" || actions["variables/TF"] != "" || plan.CanApply() != prune {
			t.Fatalf("unsafe removal plan %+v", plan)
		}
	}
}

func TestPlanOverrideExpiryChangesPlanAndNeverClaimsConvergence(t *testing.T) {
	desired, _ := Compile(definition())
	presence, field := desired.Fields[0], desired.Fields[1]
	changed := field
	changed.Value = json.RawMessage(`"temporary"`)
	expires := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	opts := PlanOptions{Manager: "git", Revision: "sha", Generation: 1, Now: expires.Add(-time.Second), Overrides: []Override{{Resource: field.Resource, Path: field.Path, ExpiresAt: expires}}}
	owners := []Ownership{{Field: field, Manager: "git"}, {Field: presence, Manager: "git"}}
	observed := ObservedState{Fields: []Field{presence, changed}}
	active, err := BuildPlan(desired, observed, owners, opts)
	if err != nil || active.Changes[1].Action != "overridden" || !active.HasDrift() {
		t.Fatalf("override hid drift: %+v %v", active, err)
	}
	opts.Now = expires
	expired, err := BuildPlan(desired, observed, owners, opts)
	if err != nil || expired.Changes[1].Action != "update" || active.Hash == expired.Hash {
		t.Fatalf("expired override remained active: %+v %v", expired, err)
	}
}

func TestPlanHashBindsAllMutationPreconditions(t *testing.T) {
	desired, _ := Compile(definition())
	field := desired.Fields[0]
	observed := ObservedState{Version: 1, Fields: []Field{field}, ResourceIDs: map[string]string{field.Resource: "app-one"}}
	owners := []Ownership{{Field: field, Manager: "git"}}
	opts := PlanOptions{Manager: "git", Revision: "sha", Generation: 1}
	base, err := BuildPlan(desired, observed, owners, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"version", "identity", "value", "owner", "revision", "generation", "prune", "adopt", "unsupported"} {
		t.Run(mutation, func(t *testing.T) {
			next := observed
			next.Fields = slices.Clone(observed.Fields)
			next.ResourceIDs = map[string]string{field.Resource: "app-one"}
			nextOwners := slices.Clone(owners)
			nextOpts := opts
			switch mutation {
			case "version":
				next.Version++
			case "identity":
				next.ResourceIDs[field.Resource] = "replacement-app"
			case "value":
				next.Fields[0].Value = json.RawMessage(`"different"`)
			case "owner":
				nextOwners[0].Manager = "terraform"
			case "revision":
				nextOpts.Revision = "new-sha"
			case "generation":
				nextOpts.Generation++
			case "prune":
				nextOpts.Prune = true
			case "adopt":
				nextOpts.Adopt = true
			case "unsupported":
				next.Unsupported = []string{"application-owned binding"}
			}
			plan, err := BuildPlan(desired, next, nextOwners, nextOpts)
			if err != nil || plan.Hash == base.Hash {
				t.Fatalf("plan did not bind %s: %v", mutation, err)
			}
		})
	}
}

func TestPlanRejectsAmbiguousObservationAndUnverifiedDesiredState(t *testing.T) {
	desired, _ := Compile(definition())
	field := desired.Fields[0]
	opts := PlanOptions{Manager: "git", Revision: "sha", Generation: 1}
	if _, err := BuildPlan(desired, ObservedState{Fields: []Field{field, field}}, nil, opts); err == nil {
		t.Fatal("duplicate observation accepted")
	}
	if _, err := BuildPlan(desired, ObservedState{}, []Ownership{{Field: field, Manager: "git"}, {Field: field, Manager: "git"}}, opts); err == nil {
		t.Fatal("duplicate owner accepted")
	}
	desired.Digest = "fabricated"
	if _, err := BuildPlan(desired, ObservedState{}, nil, opts); err == nil {
		t.Fatal("unverified desired definition accepted")
	}
}

func TestPlanOrderDoesNotChangeHash(t *testing.T) {
	d := definition()
	d.Workloads["api"].Variables["REGION"] = "eu"
	desired, _ := Compile(d)
	owners := []Ownership{{Field: desired.Fields[0], Manager: "git"}, {Field: desired.Fields[1], Manager: "git"}}
	observed := ObservedState{Fields: slices.Clone(desired.Fields)}
	opts := PlanOptions{Manager: "git", Revision: "sha", Generation: 1}
	first, _ := BuildPlan(desired, observed, owners, opts)
	slices.Reverse(observed.Fields)
	slices.Reverse(owners)
	second, _ := BuildPlan(desired, observed, owners, opts)
	if first.Hash != second.Hash {
		t.Fatal("database row order changes plan hash")
	}
}
