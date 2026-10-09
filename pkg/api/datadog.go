package api

import "sort"

// DatadogSites maps the closed set of supported Datadog sites (ADR-742) to
// their site domains. Intake hosts are derived from these and never accepted
// as free-form URLs, so a customer's API key is only ever sent to Datadog.
var DatadogSites = map[string]string{
	"us1": "datadoghq.com",
	"us3": "us3.datadoghq.com",
	"us5": "us5.datadoghq.com",
	"eu1": "datadoghq.eu",
	"ap1": "ap1.datadoghq.com",
}

// DatadogAPIKeyHeader is the header Datadog's intakes authenticate with.
const DatadogAPIKeyHeader = "DD-API-KEY"

// DatadogSiteNames returns the supported site names in a stable order.
func DatadogSiteNames() []string {
	names := make([]string, 0, len(DatadogSites))
	for name := range DatadogSites {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DatadogLogsIntakeURL returns the HTTP logs intake for a site.
func DatadogLogsIntakeURL(site string) (string, bool) {
	domain, ok := DatadogSites[site]
	if !ok {
		return "", false
	}
	return "https://http-intake.logs." + domain + "/api/v2/logs", true
}

// DatadogEventsURL returns the Events API endpoint for a site.
func DatadogEventsURL(site string) (string, bool) {
	domain, ok := DatadogSites[site]
	if !ok {
		return "", false
	}
	return "https://api." + domain + "/api/v1/events", true
}

// IsDatadogLogsIntakeURL reports whether target is exactly one of the
// supported sites' logs intakes.
func IsDatadogLogsIntakeURL(target string) bool {
	for site := range DatadogSites {
		if u, _ := DatadogLogsIntakeURL(site); u == target {
			return true
		}
	}
	return false
}
