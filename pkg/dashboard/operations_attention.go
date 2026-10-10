package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type WorkflowAttentionData struct {
	Priority, Sort                                                                                       string
	Owner                                                                                                string
	Unassigned                                                                                           bool
	DependencyStatus, RequiredOutcomeCode                                                                string
	AppSlug, Scope, TenantID, Workflow, TargetOperation, Reason, EvaluatedAt, ListURL, QueueURL, NextURL string
	BlockerCode, GroupBy, SummaryNextURL                                                                 string
	Summary                                                                                              api.OperationWorkflowAttentionSummary
	SummaryGroups                                                                                        []WorkflowAttentionSummaryGroup
	Limit                                                                                                int
	Items                                                                                                []WorkflowAttentionItem
}
type WorkflowAttentionItem struct {
	Dependencies                        []CustomerOperationWorkflowRelation
	Entry                               api.OperationWorkflowAttentionEntry
	HistoryURL, OperationURL, UpdatedAt string
}

type WorkflowAttentionSummaryGroup struct {
	Group api.OperationWorkflowAttentionGroup
	URL   string
}
