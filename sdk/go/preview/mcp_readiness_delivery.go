package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// MCP readiness notification inspection and recovery contracts.
type (
	SetMCPReadinessFallbackRequest  = api.SetMCPReadinessFallbackRequest
	MCPReadinessFallbackPolicy      = api.MCPReadinessFallbackPolicy
	MCPReadinessDeliveryRouting     = api.MCPReadinessDeliveryRouting
	MCPReadinessNotificationAction  = api.MCPReadinessNotificationAction
	MCPReadinessNotificationReceipt = api.MCPReadinessNotificationReceipt
	MCPReadinessNotificationQueue   = api.MCPReadinessNotificationQueue
	MCPReadinessDestinationDelivery = api.MCPReadinessDestinationDelivery
	MCPReadinessDestinationQueue    = api.MCPReadinessDestinationQueue
	MCPReadinessNotificationQueues  = api.MCPReadinessNotificationQueues
	MCPReadinessIncidentEvent       = api.MCPReadinessIncidentEvent
	MCPReadinessIncidentTimeline    = api.MCPReadinessIncidentTimeline
	MCPReadinessOAuthConnection     = api.MCPReadinessOAuthConnection
	MCPReadinessMonitorObservation  = api.MCPReadinessMonitorObservation
	MCPReadinessMonitorStatus       = api.MCPReadinessMonitorStatus
)
