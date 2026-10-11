package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

type (
	DurableEntityRetryRequest     = api.DurableEntityRetryRequest
	DurableEntityRetryResponse    = api.DurableEntityRetryResponse
	DurableEntityInvokeRequest    = api.DurableEntityInvokeRequest
	DurableEntityInvokeResponse   = api.DurableEntityInvokeResponse
	DurableEntityInspectRequest   = api.DurableEntityInspectRequest
	DurableEntityInspectResponse  = api.DurableEntityInspectResponse
	DurableEntityScope            = api.DurableEntityScope
	DurableEntityAlarmInspection  = api.DurableEntityAlarmInspection
	DurableEntityOutboxInspection = api.DurableEntityOutboxInspection
	DurableEntityHeadDelivery     = api.DurableEntityHeadDelivery
)

// Data recovery preserves the target's current delivery history.
type (
	DurableEntityStateExport     = api.DurableEntityStateExport
	DurableEntityRestoreRequest  = api.DurableEntityRestoreRequest
	DurableEntityRestoreResponse = api.DurableEntityRestoreResponse
)

type (
	DurableEntityBackup         = api.DurableEntityBackup
	DurableEntityBackupPage     = api.DurableEntityBackupPage
	DurableEntityBackupInfo     = api.DurableEntityBackupInfo
	DurableEntityRestorePreview = api.DurableEntityRestorePreview
)

type DurableEntityRestoreValidationResponse = api.DurableEntityRestoreValidationResponse

// DurableEntityValidatorDeploymentInfo describes a bounded deployment detail sample.
type DurableEntityValidatorDeploymentInfo = api.DurableEntityValidatorDeploymentInfo
