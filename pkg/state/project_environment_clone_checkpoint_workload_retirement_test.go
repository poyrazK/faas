package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func workloadRetirementFixture(phase string) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirement, state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	p := state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan{OperationID: "10000000-0000-4000-8000-000000000001", AccountID: "10000000-0000-4000-8000-000000000002", ProjectID: "10000000-0000-4000-8000-000000000003", SourceEnvironment: "production", TargetEnvironment: "stage", SourceRevisionHash: strings.Repeat("a", 64), SourceCompensationSHA256: strings.Repeat("b", 64), SourceCompensatedAt: at, Workloads: []state.ProjectEnvironmentCloneCheckpointRetirementWorkload{{AppID: "10000000-0000-4000-8000-000000000004", SourceDeploymentID: "10000000-0000-4000-8000-000000000005", SourceHash: strings.Repeat("c", 64)}}}
	if phase != "unmaterialized" {
		p.EnvironmentID = "10000000-0000-4000-8000-000000000006"
		p.MaterializationSHA256 = strings.Repeat("d", 64)
		p.Workloads[0].SpecID = "10000000-0000-4000-8000-000000000007"
		p.Workloads[0].SpecHash = strings.Repeat("e", 64)
	}
	if phase == "deployed" {
		p.ReleaseSetID = "10000000-0000-4000-8000-000000000008"
		p.Workloads[0].TargetDeploymentID = "10000000-0000-4000-8000-000000000009"
		p.Workloads[0].TargetSettingsHash = strings.Repeat("f", 64)
	}
	in := state.ProjectEnvironmentCloneCheckpointWorkloadRetirement{Plan: p, State: "requested", RequestedAt: at.Add(time.Minute)}
	o := state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(p), OwnerToken: p.OperationID, AllDeploymentRequestsPermanentlyFenced: true, AllSchedulerOperationsClosed: true, NoOwnedInstances: true, NoOwnedCgroups: true, NoOwnedNetworkResources: true, NoOwnedRoutes: true, NoNativePublication: true, SourceUnchanged: true, RetiredAt: at.Add(2 * time.Minute), Workloads: []state.ProjectEnvironmentCloneCheckpointRetiredWorkload{{AppID: p.Workloads[0].AppID, TargetDeploymentID: p.Workloads[0].TargetDeploymentID, DeploymentPermanentlyFenced: phase == "deployed", AllRuntimeOperationsClosed: phase == "deployed"}}}
	return in, o
}

func TestCheckpointWorkloadRetirementOriginalOwnershipAndReplay(t *testing.T) {
	for _, phase := range []string{"unmaterialized", "materialized", "deployed"} {
		t.Run(phase, func(t *testing.T) {
			in, o := workloadRetirementFixture(phase)
			if err := state.ValidateCheckpointWorkloadRetirementObservation(in, o); err != nil {
				t.Fatal(err)
			}
			in.State = "retired"
			in.ObservationSHA256 = state.CloneCheckpointSHA(o)
			if err := state.ValidateCheckpointWorkloadRetirementObservation(in, o); err != nil {
				t.Fatal("terminal replay", err)
			}
			o.RetiredAt = o.RetiredAt.Add(time.Microsecond)
			if !errors.Is(state.ValidateCheckpointWorkloadRetirementObservation(in, o), state.ErrConflict) {
				t.Fatal("changed terminal proof accepted")
			}
		})
	}
	in, o := workloadRetirementFixture("unmaterialized")
	in.Plan.Workloads = nil
	o.Workloads = nil
	o.PlanSHA256 = state.CloneCheckpointSHA(in.Plan)
	if err := state.ValidateCheckpointWorkloadRetirementObservation(in, o); err != nil {
		t.Fatal("empty original scope", err)
	}
}

func TestCheckpointWorkloadRetirementRejectsUnsafePlans(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan){
		"production target": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.TargetEnvironment = "production"
		},
		"same scope": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.SourceEnvironment = p.TargetEnvironment
		},
		"unbound source proof": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.SourceCompensationSHA256 = ""
		},
		"source deployment alias": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads[0].TargetDeploymentID = p.Workloads[0].SourceDeploymentID
		},
		"source app alias": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads[0].SpecID = p.Workloads[0].AppID
		},
		"environment alias": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.EnvironmentID = p.ProjectID },
		"release alias": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.ReleaseSetID = p.EnvironmentID
		},
		"unbound materialization": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.MaterializationSHA256 = "" },
		"missing original spec": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads[0].SpecID = ""
			p.Workloads[0].SpecHash = ""
		},
		"unbound deployment": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads[0].TargetSettingsHash = ""
		},
		"duplicate workload": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads = append(p.Workloads, p.Workloads[0])
		},
		"foreign source time": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.SourceCompensatedAt = time.Now().UTC().Add(time.Hour)
		},
		"empty materialization": func(p *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.Workloads = nil },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			in, _ := workloadRetirementFixture("deployed")
			edit(&in.Plan)
			if err := state.ValidateCheckpointWorkloadRetirementPlan(in.Plan); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unsafe plan accepted", err)
			}
		})
	}
	// A target cannot alias another workload's original source deployment either.
	in, _ := workloadRetirementFixture("deployed")
	second := in.Plan.Workloads[0]
	second.AppID = "20000000-0000-4000-8000-000000000004"
	second.SourceDeploymentID = in.Plan.Workloads[0].TargetDeploymentID
	second.TargetDeploymentID = "20000000-0000-4000-8000-000000000009"
	second.SpecID = "20000000-0000-4000-8000-000000000007"
	in.Plan.Workloads = append(in.Plan.Workloads, second)
	if !errors.Is(state.ValidateCheckpointWorkloadRetirementPlan(in.Plan), state.ErrConflict) {
		t.Fatal("cross-workload source alias accepted")
	}
}

func TestCheckpointWorkloadRetirementRequiresCompleteNativeClosure(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation){
		"queued deployment": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.AllDeploymentRequestsPermanentlyFenced = false
		},
		"queued scheduler operation": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.AllSchedulerOperationsClosed = false
		},
		"owned instance": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.NoOwnedInstances = false
		},
		"owned cgroup": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.NoOwnedCgroups = false
		},
		"owned network": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.NoOwnedNetworkResources = false
		},
		"owned route": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.NoOwnedRoutes = false
		},
		"native publication": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.NoNativePublication = false
		},
		"source changed": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.SourceUnchanged = false
		},
		"missing member": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.Workloads = nil },
		"foreign deployment": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.Workloads[0].TargetDeploymentID = "20000000-0000-4000-8000-000000000009"
		},
		"member request not fenced": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.Workloads[0].DeploymentPermanentlyFenced = false
		},
		"member queued runtime": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.Workloads[0].AllRuntimeOperationsClosed = false
		},
		"wrong request": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.RequestID = "wrong" },
		"wrong plan": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.PlanSHA256 = strings.Repeat("0", 64)
		},
		"wrong owner": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.OwnerToken = "wrong" },
		"before claim": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.RetiredAt = o.RetiredAt.Add(-2 * time.Minute)
		},
		"future time": func(o *state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.RetiredAt = time.Now().UTC().Add(time.Hour)
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			in, o := workloadRetirementFixture("deployed")
			edit(&o)
			if !errors.Is(state.ValidateCheckpointWorkloadRetirementObservation(in, o), state.ErrConflict) {
				t.Fatal("incomplete native proof accepted")
			}
		})
	}
	in, o := workloadRetirementFixture("deployed")
	in.State = "planned"
	in.RequestedAt = time.Time{}
	if !errors.Is(state.ValidateCheckpointWorkloadRetirementObservation(in, o), state.ErrConflict) {
		t.Fatal("undispatched retirement accepted")
	}
	in, o = workloadRetirementFixture("unmaterialized")
	o.Workloads[0].TargetDeploymentID = "20000000-0000-4000-8000-000000000009"
	if !errors.Is(state.ValidateCheckpointWorkloadRetirementObservation(in, o), state.ErrConflict) {
		t.Fatal("observer invented a deployment")
	}
}

func TestCheckpointWorkloadRetirementEvidenceBounds(t *testing.T) {
	in, o := workloadRetirementFixture("deployed")
	in.Plan.Workloads[0].SourceHash = strings.Repeat("x", api.CheckpointWorkloadRetirementMaxBytes)
	if !errors.Is(state.ValidateCheckpointWorkloadRetirementPlan(in.Plan), state.ErrQuotaExceeded) {
		t.Fatal("oversized original ownership accepted")
	}
	in, o = workloadRetirementFixture("deployed")
	o.RequestID = strings.Repeat("x", api.CheckpointWorkloadRetirementMaxBytes)
	if !errors.Is(state.ValidateCheckpointWorkloadRetirementObservation(in, o), state.ErrQuotaExceeded) {
		t.Fatal("oversized native proof accepted")
	}
}
