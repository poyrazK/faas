package environmentsync

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
)

func TestBuildArtifactReferenceIsImmutableCodeIdentity(t *testing.T) {
	source := api.EnvironmentWorkloadSource{Kind: "source", Directory: ".", BuildArtifactID: "018f22b4-1d51-7d55-a5d8-fdc06ec7c663", BuildArtifactHash: strings.Repeat("a", 64)}
	definition := api.EnvironmentDefinition{APIVersion: APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"app": {Source: &source}}}
	desired, err := Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(desired, ObservedState{}, nil, PlanOptions{Generation: 1, Manager: "manager", Revision: "revision", CommitSHA: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes {
		if change.Path == "source_revision" && string(change.After) != "null" {
			t.Fatal("pinned code was replaced by manifest commit")
		}
	}
	for _, mutate := range []func(*api.EnvironmentWorkloadSource){func(s *api.EnvironmentWorkloadSource) { s.BuildArtifactHash = "" }, func(s *api.EnvironmentWorkloadSource) { s.BuildArtifactID = "missing" }, func(s *api.EnvironmentWorkloadSource) { s.BuildArtifactHash = strings.Repeat("A", 64) }, func(s *api.EnvironmentWorkloadSource) {
		s.Kind = "image"
		s.Image = "registry.example/app@sha256:" + strings.Repeat("a", 64)
		s.Directory = ""
	}} {
		s := source
		mutate(&s)
		definition.Workloads["app"] = api.EnvironmentWorkload{Source: &s}
		if _, err := Compile(definition); err == nil {
			t.Fatal("invalid artifact reference accepted")
		}
	}
	definition.Workloads["app"] = api.EnvironmentWorkload{Source: &source, Runtime: json.RawMessage(`{"execution_mode":"job","entrypoint":["/bin/true"]}`), Schedule: &api.EnvironmentJobSchedule{Cron: "0 * * * *", Timezone: "UTC"}}
	scheduled, err := Compile(definition)
	if err != nil {
		t.Fatal("scheduled artifact reference rejected", err)
	}
	jobPlan, err := BuildPlan(scheduled, ObservedState{}, nil, PlanOptions{Generation: 1, Manager: "manager", Revision: "revision", CommitSHA: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range jobPlan.Changes {
		if change.Path == "source_revision" && string(change.After) != "null" {
			t.Fatal("scheduled artifact acquired the target manifest's build commit")
		}
	}
}

// Replacing code identity is an explicit source operation, not permission to
// prune configuration. The derived commit stays owned and converges to null.
func TestBuildArtifactReferenceClearsOwnedCommitWithoutPruning(t *testing.T) {
	old := api.EnvironmentWorkloadSource{Kind: "source", Directory: "."}
	pinned := old
	pinned.BuildArtifactID = "018f22b4-1d51-7d55-a5d8-fdc06ec7c663"
	pinned.BuildArtifactHash = strings.Repeat("a", 64)
	definition := api.EnvironmentDefinition{APIVersion: APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"app": {Source: &pinned}}}
	desired, err := Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	oldSource, _ := json.Marshal(old)
	oldCommit, _ := json.Marshal(strings.Repeat("b", 40))
	observed := ObservedState{Fields: []Field{{Resource: "workload/app", Path: "presence", Value: json.RawMessage("true")}, {Resource: "workload/app", Path: "source", Value: oldSource}, {Resource: "workload/app", Path: "source_revision", Value: oldCommit}}}
	var owners []Ownership
	for _, field := range observed.Fields {
		owners = append(owners, Ownership{Field: field, Manager: "manager"})
	}
	options := PlanOptions{Generation: 2, Manager: "manager", Revision: "revision", CommitSHA: strings.Repeat("c", 40)}
	plan, err := BuildPlan(desired, observed, owners, options)
	if err != nil || !plan.CanApply() {
		t.Fatalf("artifact selection required prune: %+v %v", plan, err)
	}
	cleared := false
	for i, change := range plan.Changes {
		if change.Path == "source_revision" {
			cleared = change.Action == "update" && string(change.After) == "null"
		}
		if change.Action == "remove" || change.Action == "remove_candidate" {
			t.Fatal("artifact selection pruned intent")
		}
		observed.Fields[i] = Field{Resource: change.Resource, Path: change.Path, Value: change.After}
		owners[i] = Ownership{Field: observed.Fields[i], Manager: "manager"}
	}
	if !cleared {
		t.Fatal("old build commit was retained")
	}
	stable, err := BuildPlan(desired, observed, owners, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range stable.Changes {
		if change.Action != "keep" {
			t.Fatalf("artifact identity did not converge: %+v", change)
		}
	}
	definition.Workloads["app"] = api.EnvironmentWorkload{Source: &old}
	unpin, err := Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	rebuild, err := BuildPlan(unpin, observed, owners, options)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, change := range rebuild.Changes {
		if change.Path == "source_revision" && change.Action == "update" && string(change.After) == `"`+options.CommitSHA+`"` {
			found = true
		}
	}
	if !found {
		t.Fatal("returning to source builds lost the approved commit")
	}
}
