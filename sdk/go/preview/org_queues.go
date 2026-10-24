package preview

import "github.com/poyrazK/faas/sdk/go/internal/api"

type (
	OrgQueueUsageResponse                         = api.OrgQueueUsageResponse
	ProjectEnvironmentQueueBinding                = api.ProjectEnvironmentQueueBinding
	ProjectEnvironmentQueueBindingsResponse       = api.ProjectEnvironmentQueueBindingsResponse
	QueueDeadLetterMessage                        = api.QueueDeadLetterMessage
	QueueDeadLetterResponse                       = api.QueueDeadLetterResponse
	QueueLeaseMessage                             = api.QueueLeaseMessage
	QueueLeaseReceiptFailure                      = api.QueueLeaseReceiptFailure
	QueueLeaseReceiptsRequest                     = api.QueueLeaseReceiptsRequest
	QueueLeaseReceiptsResponse                    = api.QueueLeaseReceiptsResponse
	QueueLeaseRequest                             = api.QueueLeaseRequest
	QueueLeaseResponse                            = api.QueueLeaseResponse
	QueuePeekMessage                              = api.QueuePeekMessage
	QueuePeekResponse                             = api.QueuePeekResponse
	QueueStateResponse                            = api.QueueStateResponse
	ReplaceProjectEnvironmentQueueBindingsRequest = api.ReplaceProjectEnvironmentQueueBindingsRequest
)
