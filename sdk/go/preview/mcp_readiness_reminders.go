package preview

import "github.com/gregale-dev/gregale-go/internal/api"

// Readiness incident attention and inherited reminder contracts.
type (
	MCPReadinessReminderPolicy             = api.MCPReadinessReminderPolicy
	MCPReadinessAcknowledgement            = api.MCPReadinessAcknowledgement
	MCPReadinessAttentionAction            = api.MCPReadinessAttentionAction
	MCPReadinessAttentionReceipt           = api.MCPReadinessAttentionReceipt
	MCPReadinessIncidentAttention          = api.MCPReadinessIncidentAttention
	MCPReadinessAttentionPage              = api.MCPReadinessAttentionPage
	MCPReadinessAttentionHistory           = api.MCPReadinessAttentionHistory
	MCPReadinessReminderDefaults           = api.MCPReadinessReminderDefaults
	SetMCPReadinessReminderDefaultsRequest = api.SetMCPReadinessReminderDefaultsRequest
)
