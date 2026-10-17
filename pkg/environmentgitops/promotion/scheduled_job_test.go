package promotion

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func TestScheduledBuildArtifactProposalPreservesTargetExecutionContract(t *testing.T) {
	for _, kind := range []string{"source", "dockerfile"} {
		for _, prefix := range []string{"promotion", "rollback"} {
			t.Run(kind+"/"+prefix, func(t *testing.T) {
				origin := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "reports", Environment: "staging", Workloads: map[string]api.EnvironmentWorkload{
					"report": {App: "report-job", Source: &api.EnvironmentWorkloadSource{Kind: kind, Directory: "jobs/report"}, Runtime: json.RawMessage(`{"execution_mode":"job","entrypoint":["/app/report","staging"]}`), Schedule: &api.EnvironmentJobSchedule{Cron: "0 * * * *", Timezone: "UTC"}, JobSmoke: &api.EnvironmentJobSmoke{Command: []string{"/bin/true"}, TimeoutSeconds: 30}},
				}}
				compiledOrigin, err := environmentsync.Compile(origin)
				if err != nil {
					t.Fatal(err)
				}
				origin = compiledOrigin.Definition
				targetDefinition := origin
				targetDefinition.Environment = "production"
				targetDefinition.Workloads = map[string]api.EnvironmentWorkload{}
				target := origin.Workloads["report"]
				target.Runtime = json.RawMessage(`{"execution_mode":"job","entrypoint":["/app/report","production"]}`)
				target.Schedule = &api.EnvironmentJobSchedule{Cron: "0 3 * * *", Timezone: "Europe/Istanbul"}
				target.Variables = map[string]string{"MODE": "TARGET_PRIVATE"}
				target.SecretRefs = map[string]string{"TOKEN": "secret:TARGET_TOKEN"}
				target.JobSmoke = &api.EnvironmentJobSmoke{Command: []string{"/app/check", "production"}, TimeoutSeconds: 20}
				targetDefinition.Workloads["report"] = target
				desired, err := environmentsync.Compile(targetDefinition)
				if err != nil {
					t.Fatal(err)
				}
				before, _ := json.Marshal(desired.Definition)
				artifact := api.EnvironmentGitPromotionArtifact{Resource: "workload/report", DeploymentID: "deployment", BuildID: "build", Kind: "github", BuildArtifactID: "018f22b4-1d51-7d55-a5d8-fdc06ec7c663", BuildArtifactHash: strings.Repeat("a", 64), ArtifactDigest: "sha256:" + strings.Repeat("b", 64), BuildCommitSHA: strings.Repeat("c", 40)}
				out, proposed, err := ProposeSources(origin, desired, []api.EnvironmentGitPromotionArtifact{artifact}, prefix)
				if err != nil || len(out.BlockingReasons) != 0 || len(out.Changes) != 1 {
					t.Fatal(out.BlockingReasons, err)
				}
				result := proposed.Definition.Workloads["report"]
				if result.Source.BuildArtifactID != artifact.BuildArtifactID || result.Source.BuildArtifactHash != artifact.BuildArtifactHash {
					t.Fatal("lost exact signed code identity")
				}
				result.Source = desired.Definition.Workloads["report"].Source
				if !reflect.DeepEqual(result, desired.Definition.Workloads["report"]) {
					t.Fatal("proposal replaced target execution configuration")
				}
				after, _ := json.Marshal(desired.Definition)
				if string(before) != string(after) {
					t.Fatal("proposal mutated caller target")
				}
				for _, mutate := range []func(*api.EnvironmentWorkload){func(w *api.EnvironmentWorkload) { w.Schedule = nil }, func(w *api.EnvironmentWorkload) { w.App = "other-app" }} {
					changed := desired.Definition.Workloads["report"]
					mutate(&changed)
					desired.Definition.Workloads["report"] = changed
					incompatible, err := environmentsync.Compile(desired.Definition)
					if err != nil {
						t.Fatal(err)
					}
					blocked, _, err := ProposeSources(origin, incompatible, []api.EnvironmentGitPromotionArtifact{artifact}, prefix)
					if err != nil || len(blocked.BlockingReasons) == 0 {
						t.Fatal("incompatible target accepted", err)
					}
					desired.Definition.Workloads["report"] = target
				}
			})
		}
	}
}

func TestImagePromotionRefusesChangingScheduledExecutionMode(t *testing.T) {
	in := fixture(t)
	w := in.Target.Definition.Workloads["api"]
	w.Runtime = json.RawMessage(`{"execution_mode":"job","entrypoint":["/bin/true"]}`)
	w.Schedule = &api.EnvironmentJobSchedule{Cron: "0 3 * * *", Timezone: "UTC"}
	in.Target.Definition.Workloads["api"] = w
	var err error
	in.Target, err = environmentsync.Compile(in.Target.Definition)
	if err != nil {
		t.Fatal(err)
	}
	out := complete(t, in)
	if len(out.BlockingReasons) == 0 {
		t.Fatal("unscheduled release supplied code for a scheduled target")
	}
	if _, err = ExportManifest(out, in.Target.Definition); err == nil {
		t.Fatal("incompatible schedule exported")
	}
}
