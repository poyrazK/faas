package state

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentWorkloadRuntimeSurvivesActivation(t *testing.T) {
	appID := "app-api"
	image := "registry.example.com/api@sha256:" + strings.Repeat("a", 64)
	frozen := EnvironmentWorkloadRuntime{
		SourceID: "source", EnvironmentID: "environment", RevisionID: "revision",
		Generation: 1, IntentVersion: 1, Resource: "workload/api", PlanHash: strings.Repeat("b", 64),
		AppID: appID, Scope: "production", AppType: AppTypeApp, Runtime: map[string]json.RawMessage{},
		SecretRefs: map[string]string{"API_TOKEN": "secret:TOKEN"},
		Source:     &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
	}
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	deployment := Deployment{AppID: appID, Scope: "production", Kind: DeploymentKindImage, ImageDigest: image,
		EnvironmentWorkloadRuntime: string(raw), EnvironmentWorkloadHeldValue: environmentWorkloadHeldFlag(false)}

	if deployment.EnvironmentWorkloadHeld() {
		t.Fatal("activated deployment is still held")
	}
	got, err := deployment.ScopedWorkloadRuntime()
	if err != nil {
		t.Fatalf("read frozen runtime after activation: %v", err)
	}
	if got == nil || got.Source == nil || got.Source.Image != image || got.PlanHash != frozen.PlanHash || got.SecretRefs["API_TOKEN"] != "secret:TOKEN" {
		t.Fatalf("frozen runtime was not retained after activation: %#v", got)
	}
	corrupt := frozen
	corrupt.Variables = map[string]string{"invalid-key": "value"}
	raw, err = json.Marshal(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	deployment.EnvironmentWorkloadRuntime = string(raw)
	if _, err := deployment.ScopedWorkloadRuntime(); err == nil {
		t.Fatal("persisted candidate accepted an invalid frozen environment key")
	}
	corrupt = frozen
	corrupt.SecretRefs = map[string]string{"API_TOKEN": "unreviewed:TOKEN"}
	raw, err = json.Marshal(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	deployment.EnvironmentWorkloadRuntime = string(raw)
	if _, err := deployment.ScopedWorkloadRuntime(); err == nil {
		t.Fatal("persisted candidate accepted an invalid frozen secret reference")
	}
}

func TestLegacyEnvironmentWorkloadCandidateRemainsHeld(t *testing.T) {
	deployment := Deployment{EnvironmentWorkloadRuntime: `{}`}
	if !deployment.EnvironmentWorkloadHeld() {
		t.Fatal("legacy candidate without explicit hold flag must remain held")
	}
}

func TestFrozenEnvironmentWorkloadScheduleAndVariablesAreCandidateAuthority(t *testing.T) {
	want := EnvironmentWorkloadRuntime{Schedule: &api.EnvironmentJobSchedule{Cron: "0 2 * * *", Timezone: "UTC"},
		Variables: map[string]string{"MODE": "safe"}, SecretRefs: map[string]string{"TOKEN": "secret:ACCESS_TOKEN"}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !frozenCandidateInputsMatch(raw, want) {
		t.Fatal("unchanged frozen schedule did not match its candidate inputs")
	}
	changed := want
	changedSchedule := *want.Schedule
	changedSchedule.Cron = "0 3 * * *"
	changed.Schedule = &changedSchedule
	if frozenCandidateInputsMatch(raw, changed) {
		t.Fatal("changed schedule retained the original candidate authority")
	}
	changed = want
	changed.Variables = map[string]string{"MODE": "unsafe"}
	if frozenCandidateInputsMatch(raw, changed) {
		t.Fatal("changed variables retained the original candidate authority")
	}
	changed = want
	changed.SecretRefs = map[string]string{"TOKEN": "secret:OTHER_TOKEN"}
	if frozenCandidateInputsMatch(raw, changed) {
		t.Fatal("changed secret reference retained the original candidate authority")
	}
}
