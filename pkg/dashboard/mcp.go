package dashboard

import "github.com/onebox-faas/faas/pkg/api"

// MCP views deliberately omit catalog payloads, OAuth secrets and source errors.
type MCPOperationsData struct {
	ReadinessAttention                                                                      []MCPReadinessAttentionView
	ReadinessAttentionError, ReadinessAttentionNextURL, ReadinessAttentionAfter             string
	ReadinessNotifications                                                                  []MCPReadinessNotificationView
	ReadinessNotificationsError, ReadinessNotificationsNextURL, ReadinessNotificationsAfter string
	ReadinessMaintenance                                                                    []MCPReadinessMaintenanceView
	ReadinessMaintenanceError                                                               string
	ReadinessIncidents                                                                      []api.MCPReadinessIncidentEvent
	ReadinessIncidentsError                                                                 string
	ReadinessIncidentsNextURL                                                               string
	ReadinessIncidentsBefore                                                                string
	ReadinessPolicyHistory                                                                  []api.MCPReadinessPolicy
	ReadinessHistoryUnavailable                                                             bool
	ReadinessScope                                                                          string
	ReadinessState                                                                          string
	ReadinessMonitoringState                                                                string
	ReadinessMonitoring                                                                     []MCPReadinessMonitorView
	Readiness                                                                               []MCPReadinessView
	ReadinessOverrides                                                                      []MCPReadinessOverrideView
	AlertCSRF, AlertError, AlertsURL                                                        string
	Alerts                                                                                  []MCPAlertView
	Performance                                                                             *MCPPerformanceView
	StatusMessage                                                                           string
	ActionCSRF, RetirementError, NextRetirementsURL                                         string
	Retirements                                                                             []MCPRetirementView

	AppFilter, ObservedAt, Error, HealthError                           string
	HealthComplete                                                      bool
	TotalIssues                                                         int
	Reauthorization, RenewalFailed, RetirementExpiring, IssuanceUnknown int
	Issues                                                              []MCPConnectionIssueView
	Apps                                                                []MCPOperationsAppView
	NextAppsURL, PreviousAppsURL, NextHealthURL                         string
	Evidence                                                            *MCPAdmissionView
}
type MCPConnectionIssueView struct {
	App, Role, Kind, State, ExpiresAt, Guidance, AppURL string
	Deleted                                             bool
}
type MCPOperationsAppView struct {
	PerformanceURL                                             string
	Slug, URL, DebugURL, LogsURL, EdgeRulesURL, DeploymentsURL string
	CatalogState, CatalogCheckedAt, CatalogError               string
	CatalogRoles                                               []MCPCatalogRoleView
	PolicyState, PolicyCapturedAt, PolicyError                 string
	Serving, Pending, Stale                                    int64
	Accepted, Rejected                                         *MCPAdmissionView
	AdmissionError, AdmissionScope                             string
}
type MCPCatalogRoleView struct {
	OAuthRevision, OAuthURL, BrowserRedirectURI                       string
	BrowserEnabled, RenewalRetryAllowed, ProviderConfirmationRequired bool
	Scope, Role, State, CheckedAt, ObserverState, LastSuccessAt       string
	Failures                                                          int
}
type MCPAdmissionView struct {
	ID, DeploymentID, Outcome, Reason, CapturedAt, URL string
	TrafficPercent                                     int
}

type MCPRetirementView struct {
	ID, App, Role, State, Reason, RetainUntil string
	Recoverable, CanRetry                     bool
}

type MCPAlertView struct {
	PagerDutyFallbackAcknowledgements    *api.MCPReadinessPagerDutyAcknowledgementPolicy
	PagerDutyAcknowledgements            *api.MCPReadinessPagerDutyAcknowledgementPolicy
	PagerDutyAcknowledgementsError       string
	PagerDutyAcknowledgementEvents       []api.MCPReadinessPagerDutyAcknowledgementEvent
	PagerDutyAcknowledgementEventsURL    string
	PagerDuty                            *api.MCPReadinessPagerDutyPolicy
	PagerDutyError                       string
	ReminderDefaults                     *api.MCPReadinessReminderDefaults
	ReminderDefaultsError                string
	Fallback                             *api.MCPReadinessFallbackPolicy
	FallbackError                        string
	ReadinessScope                       string
	ID, Name, Metric, State, Window, URL string
	Threshold                            float64
	MinCalls, ForMinutes                 int
	Enabled                              bool
}

type MCPReadinessView struct{ Deployment, Role, State, ObservedAt, Detail string }
type MCPReadinessOverrideView struct{ Deployment, Reason, Account, State, CreatedAt string }

type MCPReadinessMonitorView struct {
	ConnectionRole, RecoveryAction, RecoveryURL             string
	History                                                 []string
	Deployment, Role, State, Reason, LastSuccess, NextProbe string
	Failures, Threshold                                     int
}

type MCPReadinessMaintenanceView struct {
	api.MCPReadinessMaintenanceWindow
	State     string
	CanCancel bool
}

type MCPReadinessDestinationQueueView struct {
	api.MCPReadinessDestinationQueue
	RetryRequestID, AbandonRequestID string
}
type MCPReadinessNotificationView struct {
	DestinationQueues []MCPReadinessDestinationQueueView
	api.MCPReadinessNotificationQueue
	RetryRequestID, AbandonRequestID string
}

type MCPReadinessAttentionView struct {
	api.MCPReadinessIncidentAttention
	AcknowledgeRequestID, ReleaseRequestID, ConfigureRequestID, HistoryURL string
	History                                                                []api.MCPReadinessAttentionReceipt
	HistoryError                                                           string
}
