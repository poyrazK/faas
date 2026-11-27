package receivers

import "github.com/onebox-faas/faas/pkg/state"

// requestTelemetryUAFamilies is the set request_telemetry's ua_family CHECK
// allows (migration 20261121130352100, #7736): the gateway's browser and tool
// families, the sentinels, and "gregale-<source>" for each invocation source
// the platform delivers.
var requestTelemetryUAFamilies = func() map[string]bool {
	families := map[string]bool{}
	for _, family := range []string{
		"chrome", "edge", "firefox", "safari", "opera", "curl", "wget",
		"python", "go", "java", "bot", "other", "__unknown__",
	} {
		families[family] = true
	}
	for _, source := range []state.InvocationSource{
		state.InvocationAsyncInvoke, state.InvocationExclusiveOperation, state.InvocationInboundWebhook,
		state.InvocationQueue, state.InvocationDelayedTask, state.InvocationCron, state.InvocationReplay,
	} {
		families["gregale-"+string(source)] = true
	}
	return families
}()

// requestTelemetryUAFamily maps the wire family to a value the column
// accepts. An older gateway sends none ("__unknown__"); a family this
// receiver does not know is stored as "other" rather than failing the insert.
// A refused row fails its whole delivery batch and marks the gateway's
// telemetry unhealthy, which held every health-gated canary on prod while
// #7428's invocation families were missing from the CHECK (prod hunt
// 2026-11-21).
func requestTelemetryUAFamily(family string) string {
	switch {
	case family == "":
		return "__unknown__"
	case requestTelemetryUAFamilies[family]:
		return family
	default:
		return "other"
	}
}
