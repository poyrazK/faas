package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workloadRetirementFixture(phase string) (ProjectEnvironmentCloneCheckpointWorkloadRetirement, ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	p := ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan{OperationID: "10000000-0000-4000-8000-000000000001", AccountID: "10000000-0000-4000-8000-000000000002", ProjectID: "10000000-0000-4000-8000-000000000003", SourceEnvironment: "production", TargetEnvironment: "stage", SourceRevisionHash: strings.Repeat("a", 64), SourceCompensationSHA256: strings.Repeat("b", 64), SourceCompensatedAt: at, Workloads: []ProjectEnvironmentCloneCheckpointRetirementWorkload{{AppID: "10000000-0000-4000-8000-000000000004", SourceDeploymentID: "10000000-0000-4000-8000-000000000005", SourceHash: strings.Repeat("c", 64)}}}
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
	in := ProjectEnvironmentCloneCheckpointWorkloadRetirement{Plan: p, State: "requested", RequestedAt: at.Add(time.Minute)}
	o := ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation{RequestID: ProjectEnvironmentCloneCheckpointWorkloadRetirementRequestID(p), PlanSHA256: ProjectEnvironmentCloneCheckpointWorkloadRetirementPlanSHA256(p), OwnerToken: p.OperationID, AllDeploymentRequestsPermanentlyFenced: true, AllSchedulerOperationsClosed: true, NoOwnedInstances: true, NoOwnedCgroups: true, NoOwnedNetworkResources: true, NoOwnedRoutes: true, NoNativePublication: true, SourceUnchanged: true, RetiredAt: at.Add(2 * time.Minute), Workloads: []ProjectEnvironmentCloneCheckpointRetiredWorkload{{AppID: p.Workloads[0].AppID, TargetDeploymentID: p.Workloads[0].TargetDeploymentID, DeploymentPermanentlyFenced: phase == "deployed", AllRuntimeOperationsClosed: phase == "deployed"}}}
	return in, o
}

func TestCheckpointWorkloadRetirementOriginalOwnershipAndReplay(t *testing.T) {
	for _, phase := range []string{"unmaterialized", "materialized", "deployed"} {
		t.Run(phase, func(t *testing.T) {
			in, o := workloadRetirementFixture(phase)
			if err := validateCheckpointWorkloadRetirementObservation(in, o); err != nil {
				t.Fatal(err)
			}
			in.State = "retired"
			in.ObservationSHA256 = cloneCheckpointSHA(o)
			if err := validateCheckpointWorkloadRetirementObservation(in, o); err != nil {
				t.Fatal("terminal replay", err)
			}
			o.RetiredAt = o.RetiredAt.Add(time.Microsecond)
			if !errors.Is(validateCheckpointWorkloadRetirementObservation(in, o), ErrConflict) {
				t.Fatal("changed terminal proof accepted")
			}
		})
	}
	in, o := workloadRetirementFixture("unmaterialized")
	in.Plan.Workloads = nil
	o.Workloads = nil
	o.PlanSHA256 = cloneCheckpointSHA(in.Plan)
	if err := validateCheckpointWorkloadRetirementObservation(in, o); err != nil {
		t.Fatal("empty original scope", err)
	}
}

func TestCheckpointWorkloadRetirementRejectsUnsafePlans(t *testing.T) {
	cases := map[string]func(*ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan){
		"production target": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.TargetEnvironment = "production" },
		"same scope": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.SourceEnvironment = p.TargetEnvironment
		},
		"unbound source proof": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.SourceCompensationSHA256 = "" },
		"source deployment alias": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads[0].TargetDeploymentID = p.Workloads[0].SourceDeploymentID
		},
		"source app alias": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads[0].SpecID = p.Workloads[0].AppID
		},
		"environment alias":       func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.EnvironmentID = p.ProjectID },
		"release alias":           func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.ReleaseSetID = p.EnvironmentID },
		"unbound materialization": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.MaterializationSHA256 = "" },
		"missing original spec": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads[0].SpecID = ""
			p.Workloads[0].SpecHash = ""
		},
		"unbound deployment": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads[0].TargetSettingsHash = ""
		},
		"duplicate workload": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.Workloads = append(p.Workloads, p.Workloads[0])
		},
		"foreign source time": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) {
			p.SourceCompensatedAt = time.Now().UTC().Add(time.Hour)
		},
		"empty materialization": func(p *ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) { p.Workloads = nil },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			in, _ := workloadRetirementFixture("deployed")
			edit(&in.Plan)
			if err := validateCheckpointWorkloadRetirementPlan(in.Plan); !errors.Is(err, ErrConflict) {
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
	if !errors.Is(validateCheckpointWorkloadRetirementPlan(in.Plan), ErrConflict) {
		t.Fatal("cross-workload source alias accepted")
	}
}

func TestCheckpointWorkloadRetirementRequiresCompleteNativeClosure(t *testing.T) {
	cases := map[string]func(*ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation){
		"queued deployment": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.AllDeploymentRequestsPermanentlyFenced = false
		},
		"queued scheduler operation": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.AllSchedulerOperationsClosed = false
		},
		"owned instance": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.NoOwnedInstances = false },
		"owned cgroup":   func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.NoOwnedCgroups = false },
		"owned network": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.NoOwnedNetworkResources = false
		},
		"owned route":        func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.NoOwnedRoutes = false },
		"native publication": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.NoNativePublication = false },
		"source changed":     func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.SourceUnchanged = false },
		"missing member":     func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.Workloads = nil },
		"foreign deployment": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.Workloads[0].TargetDeploymentID = "20000000-0000-4000-8000-000000000009"
		},
		"member request not fenced": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.Workloads[0].DeploymentPermanentlyFenced = false
		},
		"member queued runtime": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.Workloads[0].AllRuntimeOperationsClosed = false
		},
		"wrong request": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.RequestID = "wrong" },
		"wrong plan": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.PlanSHA256 = strings.Repeat("0", 64)
		},
		"wrong owner": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) { o.OwnerToken = "wrong" },
		"before claim": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.RetiredAt = o.RetiredAt.Add(-2 * time.Minute)
		},
		"future time": func(o *ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation) {
			o.RetiredAt = time.Now().UTC().Add(time.Hour)
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			in, o := workloadRetirementFixture("deployed")
			edit(&o)
			if !errors.Is(validateCheckpointWorkloadRetirementObservation(in, o), ErrConflict) {
				t.Fatal("incomplete native proof accepted")
			}
		})
	}
	in, o := workloadRetirementFixture("deployed")
	in.State = "planned"
	in.RequestedAt = time.Time{}
	if !errors.Is(validateCheckpointWorkloadRetirementObservation(in, o), ErrConflict) {
		t.Fatal("undispatched retirement accepted")
	}
	in, o = workloadRetirementFixture("unmaterialized")
	o.Workloads[0].TargetDeploymentID = "20000000-0000-4000-8000-000000000009"
	if !errors.Is(validateCheckpointWorkloadRetirementObservation(in, o), ErrConflict) {
		t.Fatal("observer invented a deployment")
	}
}

func TestCheckpointWorkloadRetirementEvidenceBounds(t *testing.T) {
	in, o := workloadRetirementFixture("deployed")
	in.Plan.Workloads[0].SourceHash = strings.Repeat("x", api.CheckpointWorkloadRetirementMaxBytes)
	if !errors.Is(validateCheckpointWorkloadRetirementPlan(in.Plan), ErrQuotaExceeded) {
		t.Fatal("oversized original ownership accepted")
	}
	in, o = workloadRetirementFixture("deployed")
	o.RequestID = strings.Repeat("x", api.CheckpointWorkloadRetirementMaxBytes)
	if !errors.Is(validateCheckpointWorkloadRetirementObservation(in, o), ErrQuotaExceeded) {
		t.Fatal("oversized native proof accepted")
	}
}
