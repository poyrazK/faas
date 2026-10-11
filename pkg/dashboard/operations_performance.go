package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type WorkflowPerformanceData struct {
	RuleComparisons                                                      []WorkflowRuleComparison
	DecisionGroups                                                       []WorkflowDecisionOutcomeLink
	AppSlug, Scope, TenantID, Workflow, EvaluatedAt, ListURL, SummaryURL string
	Drilldown                                                            *api.OperationWorkflowPerformanceInstancesResponse
	Contributors                                                         []WorkflowPerformanceContributor
	GroupLabel, RefreshURL                                               string
	CohortChanged                                                        bool
	Summary                                                              *api.OperationWorkflowPerformanceSummary
	Cohorts                                                              []WorkflowPerformanceCohort
}
type WorkflowPerformanceCohort struct {
	Name                                              string
	StateTimeURL, BlockedTimeURL, VerificationWaitURL string
	StateGroups                                       []WorkflowStatePerformanceLink
	BlockerGroups                                     []WorkflowBlockerPerformanceLink
	VerificationGroups                                []WorkflowVerificationPerformanceLink
	api.OperationWorkflowPerformanceCohort
}

type WorkflowRuleComparison struct {
	api.OperationWorkflowRuleComparison
	Versions []WorkflowRuleVersion
}
type WorkflowRuleVersion struct {
	api.OperationWorkflowRuleVersionStatistics
	Outcomes []WorkflowRuleOutcome
}
type WorkflowRuleOutcome struct {
	api.OperationWorkflowRuleVersionOutcome
	Percent string
}
