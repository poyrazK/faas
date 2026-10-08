package state

import (
	"context"
	"time"
)

// Sealed values never appear in API responses. Jobs contain inbox identifiers,
// rather than message bodies, and a version fence protects rotated tokens.
type ManagedRealtimePushProvider struct {
	EndpointID string    `json:"-"`
	Provider   string    `json:"provider"`
	Enabled    bool      `json:"enabled"`
	Sealed     []byte    `json:"-"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type ManagedRealtimePushDevice struct {
	Fingerprint string    `json:"-"`
	EndpointID  string    `json:"-"`
	Principal   string    `json:"-"`
	Device      string    `json:"device"`
	Provider    string    `json:"provider"`
	Enabled     bool      `json:"enabled"`
	Version     int64     `json:"version"`
	Sealed      []byte    `json:"-"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type ManagedRealtimePushDelivery struct {
	NotBefore      time.Time `json:"not_before"`
	CollapseKey    string    `json:"collapse_key,omitempty"`
	HardExpiresAt  time.Time `json:"-"`
	Priority       string    `json:"priority"`
	GroupKey       string    `json:"group_key,omitempty"`
	GroupLabel     string    `json:"group_label,omitempty"`
	DigestID       string    `json:"digest_id,omitempty"`
	DigestCount    int       `json:"digest_count,omitempty"`
	DigestAt       time.Time `json:"-"`
	Category       string    `json:"category"`
	ExpiresAt      time.Time `json:"expires_at"`
	ID             string    `json:"id"`
	EndpointID     string    `json:"-"`
	Principal      string    `json:"-"`
	Device         string    `json:"device"`
	Provider       string    `json:"provider"`
	Version        int64     `json:"-"`
	MessageID      string    `json:"message_id"`
	Sequence       int64     `json:"sequence"`
	Status         string    `json:"status"`
	Attempts       int       `json:"attempts"`
	StatusCode     int       `json:"status_code"`
	Code           string    `json:"code,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	NextAttempt    time.Time `json:"next_attempt"`
	Lease          string    `json:"-"`
	LeaseUntil     time.Time `json:"-"`
	Config, Target []byte    `json:"-"`
}
type ManagedRealtimePushStore interface {
	PutManagedRealtimePushProvider(context.Context, ManagedRealtimePushProvider) error
	ListManagedRealtimePushProviders(context.Context, string) ([]ManagedRealtimePushProvider, error)
	PutManagedRealtimePushDevice(context.Context, ManagedRealtimePushDevice) error
	ListManagedRealtimePushDevices(context.Context, string, string) ([]ManagedRealtimePushDevice, error)
	DeleteManagedRealtimePushDevice(context.Context, string, string, string) error
	ListManagedRealtimePushDeliveries(context.Context, string, string) ([]ManagedRealtimePushDelivery, error)
	ClaimManagedRealtimePush(context.Context, int) ([]ManagedRealtimePushDelivery, error)
	ManagedRealtimePushLeaseActive(context.Context, string, string) (bool, error)
	CompleteManagedRealtimePush(context.Context, string, string, int, string, bool, bool) error
}

func validPushProvider(p string) bool { return p == "fcm" || p == "apns" || p == "webpush" }
func pushDeviceKey(ep, principal, device string) (managedRealtimeDurableCursorKey, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return managedRealtimeDurableCursorKey{}, err
	}
	if err = validateManagedRealtimeDurableCursor(ep, key, device, key, 0); err != nil {
		return managedRealtimeDurableCursorKey{}, err
	}
	return managedRealtimeDurableCursorKey{endpointID: ep, principal: key, subscription: device, channel: key}, nil
}

var pushRetryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 20 * time.Minute, time.Hour, 6 * time.Hour}

func validPushFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func notificationDeadline(created time.Time, ttl int) *time.Time {
	if ttl == 0 {
		return nil
	}
	end := created.Add(time.Duration(ttl) * time.Second)
	return &end
}
