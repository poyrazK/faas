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
		if change.Path == "source_revision" {
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
	if _, err := Compile(definition); err == nil {
		t.Fatal("unsupported scheduled reuse accepted")
	}
}
