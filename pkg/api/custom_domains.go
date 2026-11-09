package api

import "time"

// VerificationExpiredAt also works with a cached response as its lease ages.
// An absent deadline preserves compatibility with older API servers.
func (d CustomDomainResponse) VerificationExpiredAt(at time.Time) bool {
	if d.Verified {
		return false
	}
	if d.VerificationExpired {
		return true
	}
	expires, err := time.Parse(time.RFC3339Nano, d.VerificationExpiresAt)
	return err == nil && !expires.After(at)
}
