package state

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkloadProgressAttributesMissingJobProofWithoutChangingAuthority(t *testing.T) {
	worker, job, retained, candidate, instance, request := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "prepared", ResourceIDs: map[string]string{"workload/worker": worker, "workload/report": job},
		Members: []EnvironmentWorkloadGraphMember{
			{Resource: "workload/worker", AppID: worker, ExecutionMode: "worker", RetainedDeployments: []string{retained}, Variables: map[string]string{"PRIVATE": "do-not-expose"}},
			{Resource: "workload/report", AppID: job, CandidateDeploymentID: candidate, ExecutionMode: "job", JobSmokeConfigured: true, ScheduleConfigured: true, JobID: uuid.NewString()},
		}}
	targets := map[string]string{"workload/worker": retained, "workload/report": candidate}
	proof := EnvironmentQualificationJobSmokeReceipt{RequestID: request, GraphID: graph.ID, Resource: "workload/report", InstanceID: instance, Attempt: 2,
		PolicyID: environmentQualificationJobSmokePolicyID, PolicySHA256: strings.Repeat("a", 64), ResultSHA256: qualificationJobSmokeResultSHA256(0, "succeeded", 0)}
	proofs := map[string]EnvironmentQualificationJobSmokeReceipt{candidate: proof}
	configs := map[string]EnvironmentQualificationConfigReceipt{instance: {RequestID: request, GraphID: graph.ID, InstanceID: instance, Attempt: 2, APIEnvSHA256: strings.Repeat("b", 64)}}
	preparation := func(member EnvironmentWorkloadGraphMember) (string, string, error) {
		return string(DeployLive), string(BuildSucceeded), nil
	}
	before := graphActivationEvidenceWithReleaseTargets(graph, nil, nil, nil, configs, nil, proofs, targets)
	rows, err := environmentWorkloadProgress(graph, nil, nil, nil, configs, nil, proofs, targets, false, preparation)
	if err != nil || len(rows) != 2 || rows[0].Resource != "workload/report" || rows[0].Qualification != "complete" || rows[1].Qualification != "complete" || rows[0].Activation != "complete" || rows[0].Serving != "unknown" {
		t.Fatalf("incorrect workload facts: %+v %v", rows, err)
	}
	encoded, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(encoded), "do-not-expose") || strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("frozen values leaked into progress")
	}
	if after := graphActivationEvidenceWithReleaseTargets(graph, nil, nil, nil, configs, nil, proofs, targets); !reflect.DeepEqual(before, after) {
		t.Fatal("status projection changed authority")
	}
	config := configs[instance]
	config.Attempt--
	configs[instance] = config
	rows, err = environmentWorkloadProgress(graph, nil, nil, nil, configs, nil, proofs, targets, false, preparation)
	if err != nil || rows[0].Qualification != "pending" || rows[1].Qualification != "complete" {
		t.Fatalf("one stale receipt changed unrelated workload: %+v %v", rows, err)
	}
}

func TestWorkloadProgressBuildFailuresAndUnavailablePreparationStayExplicit(t *testing.T) {
	graph := EnvironmentWorkloadGraph{ID: uuid.NewString(), Phase: "preparing", Members: []EnvironmentWorkloadGraphMember{{Resource: "workload/report", AppID: uuid.NewString(), CandidateDeploymentID: uuid.NewString(), ExecutionMode: api.ExecutionModeJob}}}
	for _, check := range []struct{ build, want string }{{"failed", "failed"}, {"cancelled", "failed"}, {"running", "pending"}, {"queued", "pending"}, {"", "unknown"}} {
		rows, err := environmentWorkloadProgress(graph, nil, nil, nil, nil, nil, nil, nil, false,
			func(EnvironmentWorkloadGraphMember) (string, string, error) { return "", check.build, nil })
		if err != nil || rows[0].Preparation != check.want || rows[0].Qualification == "complete" || rows[0].Serving == "complete" {
			t.Fatalf("build=%s rows=%+v err=%v", check.build, rows, err)
		}
	}
	want := errors.New("read unavailable")
	if _, err := environmentWorkloadProgress(graph, nil, nil, nil, nil, nil, nil, nil, false,
		func(EnvironmentWorkloadGraphMember) (string, string, error) { return "", "", want }); !errors.Is(err, want) {
		t.Fatalf("read error hidden: %v", err)
	}
}
