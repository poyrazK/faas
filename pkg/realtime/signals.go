package realtime

import "time"

const ManagedRealtimeSignalMaxTTL = 30 * time.Second

// Named signals replace the sender's previous value under the same name.
// Expiry is stamped by the realtime node; clients cannot supply timestamps.
func validSignalName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, char := range name {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' && char != '-' && char != '.' && char != ':' {
			return false
		}
	}
	return true
}

func validSignalExpiry(name string, updated time.Time, expires *time.Time) bool {
	if name == "" {
		return expires == nil
	}
	if !validSignalName(name) || updated.IsZero() || expires == nil {
		return false
	}
	lifetime := expires.Sub(updated)
	return lifetime >= 0 && lifetime <= ManagedRealtimeSignalMaxTTL
}
