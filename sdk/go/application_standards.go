package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Wire types for application standards, reviews and inspection.
type (
	ApproveApplicationStandardReviewRequest        = api.ApproveApplicationStandardReviewRequest
	ControlApplicationStandardOperationRequest     = api.ControlApplicationStandardOperationRequest
	SetApplicationStandardLocalIntentRequest       = api.SetApplicationStandardLocalIntentRequest
	ApproveApplicationStandardExceptionRequest     = api.ApproveApplicationStandardExceptionRequest
	RevokeApplicationStandardExceptionRequest      = api.RevokeApplicationStandardExceptionRequest
	CreateApplicationStandardVersionRequest        = api.CreateApplicationStandardVersionRequest
	ApplicationStandardVersion                     = api.ApplicationStandardVersion
	ApplicationStandardList                        = api.ApplicationStandardList
	ApplicationStandardEnrollment                  = api.ApplicationStandardEnrollment
	ApplicationStandardReviewRequest               = api.ApplicationStandardReviewRequest
	ApplicationStandardReview                      = api.ApplicationStandardReview
	ApplicationStandardReviewBlocker               = api.ApplicationStandardReviewBlocker
	ApplicationStandardReviewedApp                 = api.ApplicationStandardReviewedApp
	ApplicationStandardOperation                   = api.ApplicationStandardOperation
	ApplicationStandardOperationTarget             = api.ApplicationStandardOperationTarget
	ApplicationStandardException                   = api.ApplicationStandardException
	ApplicationStandardExceptionList               = api.ApplicationStandardExceptionList
	CreateApplicationStandardLogDestinationRequest = api.CreateApplicationStandardLogDestinationRequest
	ApplicationStandardLogDestination              = api.ApplicationStandardLogDestination
	ApplicationStandardLogDestinationList          = api.ApplicationStandardLogDestinationList
	CreateApplicationStandardPublisherRequest      = api.CreateApplicationStandardPublisherRequest
	ApplicationStandardPublisher                   = api.ApplicationStandardPublisher
	ApplicationStandardPublisherList               = api.ApplicationStandardPublisherList
	ApplicationStandardSettings                    = api.ApplicationStandardSettings
	ApplicationStandardDefinition                  = api.ApplicationStandardDefinition
	ApplicationStandardRule                        = api.ApplicationStandardRule
	ApplicationStandardAdoption                    = api.ApplicationStandardAdoption
	ApplicationStandardSource                      = api.ApplicationStandardSource
	ApplicationStandardViolation                   = api.ApplicationStandardViolation
	ApplicationStandardEffective                   = api.ApplicationStandardEffective
)
