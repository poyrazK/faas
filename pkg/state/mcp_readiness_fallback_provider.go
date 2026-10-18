package state

import "github.com/onebox-faas/faas/pkg/api"

func readinessFallbackProvider(c MCPReadinessFallbackConfig) string {
	if c.Provider == "pagerduty" {
		return "pagerduty"
	}
	return "webhook"
}
func fallbackPagerDuty(c MCPReadinessFallbackConfig) MCPReadinessPagerDutyConfig {
	return MCPReadinessPagerDutyConfig{Enabled: c.Enabled, Region: c.Region, IntegrationKeySealed: c.IntegrationKeySealed, Fingerprint: c.Fingerprint}
}
func readinessFallbackDestination(c MCPReadinessFallbackConfig) readinessDestination {
	if c.Enabled && c.Provider == "pagerduty" {
		endpoint, _ := api.MCPReadinessPagerDutyEndpointForRegion(c.Region)
		return readinessDestination{Provider: "pagerduty", URL: endpoint, ReceiverID: c.Fingerprint, Secret: append([]byte(nil), c.IntegrationKeySealed...)}
	}
	return readinessDestination{URL: c.WebhookURL, Secret: append([]byte(nil), c.WebhookSecretSealed...)}
}
func sameReadinessPagerDuty(primary MCPReadinessPagerDutyConfig, fallback MCPReadinessFallbackConfig) bool {
	return primary.Enabled && fallback.Enabled && fallback.Provider == "pagerduty" && readinessPagerDutyRegion(primary) == readinessPagerDutyRegion(fallbackPagerDuty(fallback)) && primary.Fingerprint == fallback.Fingerprint
}
