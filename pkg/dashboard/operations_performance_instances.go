package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type WorkflowPerformanceContributor struct {
	GapGroups                                      []WorkflowGapProgressGroupLink
	Entry                                          api.OperationWorkflowPerformanceInstance
	HistoryURL, OperationURL, DecisionOperationURL string
}
type WorkflowStatePerformanceLink struct {
	api.OperationWorkflowStatePerformance
	URL string
}
type WorkflowBlockerPerformanceLink struct {
	api.OperationWorkflowBlockerPerformance
	URL string
}
type WorkflowVerificationPerformanceLink struct {
	api.OperationWorkflowVerificationPerformance
	URL string
}

type WorkflowDecisionOutcomeLink struct {
	api.OperationWorkflowDecisionOutcomeGroup
	CompletionURL, BlockedURL string
}
