package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type WorkflowPerformanceData struct {
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
