package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Application-scoped producer keys recover original retained acceptance receipts.
type (
	AppEventPublicationVerification = api.AppEventPublicationVerification
	AppEventPublishStatusQuery      = api.AppEventPublishStatusQuery
	AppEventPublishStatusResponse   = api.AppEventPublishStatusResponse
	AppEventPublishStatusEvidence   = api.AppEventPublishStatusEvidence
	AppPublishEventRequest          = api.AppPublishEventRequest
	AppPublishEventResponse         = api.AppPublishEventResponse
	AppPublishedEventReceipt        = api.AppPublishedEventReceipt
)
