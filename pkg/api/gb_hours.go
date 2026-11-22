package api

// MBSecondsPerGBHour is one billed GB-hour in usage_minutes.mb_seconds.
// Billing, the account summary and app costs all count a GB as 1024 MB
// (UsageSummary.UsedGBHours, OverageMillicentsPerGBHour); every surface
// that prints GB-hours must use the same unit so per-app rows add up to
// the billed total.
const MBSecondsPerGBHour = 1024 * 3600

// GBHoursFromMBSeconds converts metered MB-seconds to billed GB-hours.
func GBHoursFromMBSeconds(mbSeconds int64) float64 {
	return float64(mbSeconds) / MBSecondsPerGBHour
}
