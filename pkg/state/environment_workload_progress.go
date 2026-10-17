package state

import (
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// This projection is used only by status reads. It reuses receipt validation
// without changing the graph's authority or exposing frozen environment values.
func environmentWorkloadProgress(graph EnvironmentWorkloadGraph,
	captures map[string]EnvironmentQualificationSnapshotReceipt, restores map[string]EnvironmentQualificationRestoreReceipt,
	smokes map[string]EnvironmentQualificationSmokeReceipt, configs map[string]EnvironmentQualificationConfigReceipt,
	ready map[string]EnvironmentQualificationFrameworkReadyReceipt, jobSmokes map[string]EnvironmentQualificationJobSmokeReceipt,
	targets map[string]string, serving bool,
	preparation func(EnvironmentWorkloadGraphMember) (string, string, error)) ([]api.EnvironmentWorkloadProgress, error) {
	result := make([]api.EnvironmentWorkloadProgress, 0, len(graph.Members))
	for _, member := range graph.Members {
		one := graph
		one.Members = []EnvironmentWorkloadGraphMember{member}
		evidence := graphActivationEvidenceWithReleaseTargets(one, captures, restores, smokes, configs, ready, jobSmokes, targets)
		progress := api.EnvironmentWorkloadProgress{Resource: member.Resource, AppID: member.AppID, DeploymentID: member.CandidateDeploymentID,
			JobID: member.JobID, ExecutionMode: member.ExecutionMode, Preparation: "unknown", Qualification: "pending", Activation: "pending", Serving: "unknown",
			BlockingReasons: []string{}}
		status, build, err := preparation(member)
		if err != nil {
			return nil, err
		}
		switch build {
		case "queued", "running", "succeeded", "failed", "cancelled":
			progress.BuildStatus = build
		}
		if graph.Phase == "prepared" && status != "" || evidence.RetainedWorkloadsRecorded > 0 {
			progress.Preparation = "complete"
		}
		if status == string(DeployFailed) || build == "failed" || build == "cancelled" {
			progress.Preparation = "failed"
			progress.BlockingReasons = append(progress.BlockingReasons, "environment_workload_build_failed")
		} else if build == "queued" || build == "running" {
			progress.Preparation = "pending"
		}
		if evidence.Qualified && progress.Preparation == "complete" {
			progress.Qualification = "complete"
		}
		if evidence.Activated {
			progress.Activation = "complete"
		}
		if serving && evidence.Qualified && evidence.Activated && progress.Preparation == "complete" {
			progress.Serving = "complete"
		}
		for _, code := range evidence.BlockingReasons {
			// A one-member projection omits service dependencies. Only the
			// whole graph can assess production adapter/binding support.
			if code == "environment_workload_production_execution_adapter_missing" || code == "environment_service_binding_contract_unsupported" {
				continue
			}
			if strings.HasPrefix(code, "environment_") {
				progress.BlockingReasons = append(progress.BlockingReasons, code)
			}
		}
		if serving {
			progress.BlockingReasons = slices.DeleteFunc(progress.BlockingReasons, func(code string) bool { return code == "environment_serving_evidence_missing" })
		}
		slices.Sort(progress.BlockingReasons)
		progress.BlockingReasons = slices.Compact(progress.BlockingReasons)
		result = append(result, progress)
	}
	slices.SortFunc(result, func(a, b api.EnvironmentWorkloadProgress) int { return strings.Compare(a.Resource, b.Resource) })
	return result, nil
}

// Cache progress is advisory. A hit cannot complete any qualification stage.
func environmentBuildCacheProgress(progress *api.EnvironmentWorkloadProgress, build Build) {
	switch build.CacheStatus {
	case "hit", "miss", "invalidated":
		progress.BuildCacheStatus = build.CacheStatus
		if len(build.CacheKeySHA256) == 64 {
			progress.BuildCacheKeySHA256 = build.CacheKeySHA256
		}
	}
}
