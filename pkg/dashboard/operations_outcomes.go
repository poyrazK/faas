package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type WorkflowOutcomesData struct {
	AppSlug, Scope, TenantID, Workflow, Code, GroupBy, EvaluatedAt, ListURL, QueueURL, NextURL, SummaryNextURL string
	Limit                                                                                                      int
	Summary                                                                                                    api.OperationWorkflowOutcomeSummary
	Groups                                                                                                     []WorkflowOutcomeSummaryGroup
	Items                                                                                                      []WorkflowOutcomeItem
}
type WorkflowOutcomeSummaryGroup struct {
	Group api.OperationWorkflowOutcomeGroup
	URL   string
}
type WorkflowOutcomeItem struct {
	Entry                    api.OperationWorkflowOutcomeEntry
	HistoryURL, OperationURL string
}
