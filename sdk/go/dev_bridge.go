package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Dev Bridge is an operator-gated development capability. Creation returns
// secrets once; callers must not log or persist them.
type (
	CreateDevBridgeRequest        = api.CreateDevBridgeRequest
	CreateDevBridgeResponse       = api.CreateDevBridgeResponse
	DevBridgeDependency           = api.DevBridgeDependency
	DevBridgeScope                = api.DevBridgeScope
	DevBridgeSession              = api.DevBridgeSession
	DevBridgeCredentials          = api.DevBridgeCredentials
	ReplayDevBridgeWebhookRequest = api.ReplayDevBridgeWebhookRequest
	DevBridgeWebhookReplay        = api.DevBridgeWebhookReplay
	DevBridgeSessionSummary       = api.DevBridgeSessionSummary
	ListDevBridgesResponse        = api.ListDevBridgesResponse
	DevBridgeActivity             = api.DevBridgeActivity
	DevBridgeRequestRecord        = api.DevBridgeRequestRecord
)
