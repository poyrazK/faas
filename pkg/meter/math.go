package meter

import "time"

// GBHours converts mb_seconds to GB-RAM-hours. 1 GB = 1024 MB; 1 hour = 3600 s.
//
// GBHours is a pure arithmetic helper kept separate from the sampler /
// aggregator so tests can pin down the rounding. Spec §10: included quotas
// are integer GB-hours per month, so the floor is appropriate — a 256 MB +
// 8 MB Hobby instance resident for one second contributes ≈ 7.3e-5 GB-h.
func GBHours(mbSeconds int64) float64 {
	return float64(mbSeconds) / 1024.0 / 3600.0
}

// MBSecondsPerMinute is the billable mb_seconds one instance accumulates in
// one minute when its admission-time RAM stays resident for the whole
// interval. Used by the sampler.
func MBSecondsPerMinute(admissionMB int) int64 {
	return int64(admissionMB) * 60
}

// AccountMonthKey truncates t to the start of its UTC month. Used by the
// aggregator to look up a per-month quota band.
func AccountMonthKey(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// MinuteKey truncates t to the start of its UTC minute. The sample loop
// runs on minute boundaries and stamps every (instance, minute) row with
// this key so the SQL PK (instance_id, minute) is unique.
func MinuteKey(t time.Time) time.Time {
	return t.UTC().Truncate(time.Minute)
}
