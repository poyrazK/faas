package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Application-scoped producer keys recover original retained acceptance receipts.
type (
	AppPublishEventRequest   = api.AppPublishEventRequest
	AppPublishEventResponse  = api.AppPublishEventResponse
	AppPublishedEventReceipt = api.AppPublishedEventReceipt
)
