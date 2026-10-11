package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type WorkflowPerformanceData struct {
	ActiveConditionGroups                                                []WorkflowActiveConditionLink
	ConditionGroups                                                      []WorkflowConditionOutcomeLink
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
	MultiVersionExamples []WorkflowRuleOverlap
	api.OperationWorkflowRuleComparison
	Versions []WorkflowRuleVersion
}
type WorkflowRuleVersion struct {
	SingleVersion *WorkflowRuleCohort
	api.OperationWorkflowRuleVersionStatistics
	Outcomes []WorkflowRuleOutcome
}
type WorkflowRuleOutcome struct {
	api.OperationWorkflowRuleVersionOutcome
	Percent string
}

type WorkflowRuleCohort struct {
	api.OperationWorkflowRuleVersionCohort
	Outcomes []WorkflowRuleOutcome
}
type WorkflowRuleOverlap struct {
	api.OperationWorkflowRuleVersionOverlap
	HistoryURL string
	Evidence   []WorkflowRuleEvidence
}
type WorkflowRuleEvidence struct {
	api.OperationWorkflowDecisionOutcomeEvidence
	OperationURL string
}

type WorkflowConditionOutcomeLink struct {
	api.OperationWorkflowConditionOutcomeGroup
	CompletionURL, BlockedURL string
}

type WorkflowActiveConditionLink struct {
	api.OperationWorkflowActiveConditionGroup
	ElapsedURL, BlockedURL string
}
