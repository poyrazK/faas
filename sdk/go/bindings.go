package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Shared app binding metadata returned by Client.GetAppBindingInventory.
type (
	AppBindingInventory          = api.AppBindingInventory
	AppBindingInventoryItem      = api.AppBindingInventoryItem
	BindingApplicationAdoption   = api.BindingApplicationAdoption
	BindingApplicationAckTarget  = api.BindingApplicationAckTarget
	BindingAdoptionCounts        = api.BindingAdoptionCounts
	BindingInventoryIssue        = api.BindingInventoryIssue
	BindingVerification          = api.BindingVerification
	BindingVerificationCheck     = api.BindingVerificationCheck
	BindingRuntimeFreshness      = api.BindingRuntimeFreshness
	BindingRuntimeDeployment     = api.BindingRuntimeDeployment
	BindingRuntimeInstanceCounts = api.BindingRuntimeInstanceCounts
	BindingRefresh               = api.BindingRefresh
	BindingCheckFinding          = api.BindingCheckFinding
	BindingCheckBindingResult    = api.BindingCheckBindingResult
	BindingCheckReport           = api.BindingCheckReport
	BindingPromotionRequest      = api.BindingPromotionRequest
	BindingPromotionResponse     = api.BindingPromotionResponse
	OutboundBindingProbePolicy   = api.OutboundBindingProbePolicy
)

const (
	BindingTypeService       = api.BindingTypeService
	BindingTypePostgres      = api.BindingTypePostgres
	BindingTypeObjectStorage = api.BindingTypeObjectStorage
	BindingTypeQueue         = api.BindingTypeQueue
	BindingTypeOutbound      = api.BindingTypeOutbound
)
