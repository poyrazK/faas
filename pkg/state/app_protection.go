package state

import (
	"context"
	"encoding/json"
	"time"
)

// App protection enrollment states (ADR-20261116120000). An app with no
// app_protection row was never enrolled.
const (
	AppProtectionOn  = "on"
	AppProtectionOff = "off"
)

// AppProtectionItem names one object of the protection baseline.
type AppProtectionItem string

// The baseline items, in the order status reports them. The set matches the
// app_protection_objects.item CHECK.
const (
	AppProtectionAvailabilitySLO       AppProtectionItem = "availability_slo"
	AppProtectionLatencySLO            AppProtectionItem = "latency_slo"
	AppProtectionHealthCheck           AppProtectionItem = "health_check"
	AppProtectionEmailChannel          AppProtectionItem = "email_channel"
	AppProtectionAvailabilityBurnAlert AppProtectionItem = "availability_burn_alert"
	AppProtectionLatencyBurnAlert      AppProtectionItem = "latency_burn_alert"
	AppProtectionHealthAlert           AppProtectionItem = "health_alert"
	AppProtectionAvailabilityGate      AppProtectionItem = "availability_gate"
)

// AppProtectionItems lists every baseline item in status order.
var AppProtectionItems = []AppProtectionItem{
	AppProtectionAvailabilitySLO,
	AppProtectionLatencySLO,
	AppProtectionHealthCheck,
	AppProtectionEmailChannel,
	AppProtectionAvailabilityBurnAlert,
	AppProtectionLatencyBurnAlert,
	AppProtectionHealthAlert,
	AppProtectionAvailabilityGate,
}

// ValidAppProtectionItem reports whether item is a baseline item.
func ValidAppProtectionItem(item AppProtectionItem) bool {
	for _, known := range AppProtectionItems {
		if item == known {
			return true
		}
	}
	return false
}

// AppProtection is an app's enrollment in the protection baseline.
type AppProtection struct {
	AppID     string
	AccountID string
	State     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AppProtectionObject is one object the baseline created for an app.
// ObjectID is the created object's id (a release gate's name, which is its
// key). Written holds the values the baseline wrote, so the reconciler can
// tell an object the customer edited from one it still owns.
type AppProtectionObject struct {
	AppID     string
	Item      AppProtectionItem
	ObjectID  string
	Written   json.RawMessage
	CreatedAt time.Time
}

// AppProtectionStore keeps protection-baseline enrollment and the objects
// the baseline created.
type AppProtectionStore interface {
	// GetAppProtection returns the app's enrollment; ok is false when the
	// app was never enrolled.
	GetAppProtection(ctx context.Context, appID string) (AppProtection, bool, error)
	// SetAppProtection creates or updates the app's enrollment state. It
	// returns ErrNotFound when the app or account does not exist and
	// ErrInvalidArgument for an unknown state.
	SetAppProtection(ctx context.Context, appID, accountID, state string) (AppProtection, error)
	// RecordAppProtectionObject records an object the baseline created. A
	// second record for the same (app, item) replaces the first. It returns
	// ErrNotFound when the app is not enrolled.
	RecordAppProtectionObject(ctx context.Context, obj AppProtectionObject) error
	// ListAppProtectionObjects returns the recorded objects in item order.
	ListAppProtectionObjects(ctx context.Context, appID string) ([]AppProtectionObject, error)
}

// validAppProtectionObject mirrors app_protection_objects' CHECKs.
func validAppProtectionObject(obj AppProtectionObject) bool {
	return obj.AppID != "" && ValidAppProtectionItem(obj.Item) && obj.ObjectID != "" && len(obj.ObjectID) <= 128
}

// appProtectionItemRank orders recorded objects as AppProtectionItems does.
func appProtectionItemRank(item AppProtectionItem) int {
	for i, known := range AppProtectionItems {
		if item == known {
			return i
		}
	}
	return len(AppProtectionItems)
}

// appProtectionWritten defaults an empty Written to an empty object.
func appProtectionWritten(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
