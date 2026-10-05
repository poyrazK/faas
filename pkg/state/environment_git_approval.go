package state

import "github.com/onebox-faas/faas/pkg/gitapproval"

func cloneEnvironmentGitApproval(record EnvironmentGitRevisionApproval) EnvironmentGitRevisionApproval {
	record.Evidence.Reviews = append([]gitapproval.ReviewEvidence(nil), record.Evidence.Reviews...)
	return record
}
