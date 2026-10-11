package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type WorkflowReadinessQueueItem struct {
	Entry                    api.OperationWorkflowReadinessQueueEntry
	HistoryURL, OperationURL string
}
type WorkflowReadinessQueueData struct {
	Options                                                                        api.OperationWorkflowReadinessQueueOptions
	Result                                                                         api.OperationWorkflowReadinessQueueResponse
	Items                                                                          []WorkflowReadinessQueueItem
	ListURL, QueueURL                                                              string
	SnapshotJSON, CompareURL, SubscriptionsURL, FollowUpsURL, TimelineURL, CaseURL string
}
