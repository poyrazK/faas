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
// NextReconcileAt is when the reconciler next looks at the app.
type AppProtection struct {
	AppID           string
	AccountID       string
	State           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	NextReconcileAt time.Time
}

// Baseline item record statuses.
const (
	// AppProtectionObjectCreated: the baseline made the object. It is never
	// made again, so a customer who deletes it keeps it deleted.
	AppProtectionObjectCreated = "created"
	// AppProtectionObjectSkipped: the object could not be made yet; Reason
	// says why. The reconciler tries again later.
	AppProtectionObjectSkipped = "skipped"
)

// AppProtectionObject is one baseline item's record. A created item has
// the object's id and the values the baseline wrote, so a later slice can
// tell an object the customer edited from one the baseline still owns. A
// skipped item has a Reason and no object.
type AppProtectionObject struct {
	AppID     string
	Item      AppProtectionItem
	Status    string
	ObjectID  string
	Reason    string
	Written   json.RawMessage
	CreatedAt time.Time
}

// AppProtectionStore keeps protection-baseline enrollment and the objects
// the baseline created.
type AppProtectionStore interface {
	// GetAppProtection returns the app's enrollment; ok is false when the
	// app was never enrolled.
	GetAppProtection(ctx context.Context, appID string) (AppProtection, bool, error)
	// SetAppProtection creates or updates the app's enrollment state and
	// makes its next reconcile due now. It returns ErrNotFound when the app
	// or account does not exist and ErrInvalidArgument for an unknown state.
	SetAppProtection(ctx context.Context, appID, accountID, state string) (AppProtection, error)
	// RecordAppProtectionObject records a baseline item (Status empty means
	// created). A second record for the same (app, item) replaces the
	// first, except that a created record is never replaced: that reports
	// ErrConflict and keeps it. It returns ErrNotFound when the app is not
	// enrolled.
	RecordAppProtectionObject(ctx context.Context, obj AppProtectionObject) error
	// ListAppProtectionObjects returns the recorded items in item order.
	ListAppProtectionObjects(ctx context.Context, appID string) ([]AppProtectionObject, error)
	// ClaimAppProtectionReconciles leases up to limit enrolled ('on') apps
	// whose next reconcile is due and whose lease, if any, has expired.
	ClaimAppProtectionReconciles(ctx context.Context, limit int, lease time.Duration) ([]AppProtection, error)
	// FinishAppProtectionReconcile releases the app's lease and schedules
	// its next reconcile.
	FinishAppProtectionReconcile(ctx context.Context, appID string, next time.Time) error
}

// normalizeAppProtectionObject defaults Status to created and Written to
// {}, and reports whether obj satisfies app_protection_objects' CHECKs.
func normalizeAppProtectionObject(obj AppProtectionObject) (AppProtectionObject, bool) {
	if obj.Status == "" {
		obj.Status = AppProtectionObjectCreated
	}
	if len(obj.Written) == 0 {
		obj.Written = json.RawMessage(`{}`)
	}
	if obj.AppID == "" || !ValidAppProtectionItem(obj.Item) || len(obj.ObjectID) > 128 || len(obj.Reason) > 64 || !json.Valid(obj.Written) {
		return obj, false
	}
	switch obj.Status {
	case AppProtectionObjectCreated:
		return obj, obj.ObjectID != "" && obj.Reason == ""
	case AppProtectionObjectSkipped:
		return obj, obj.ObjectID == "" && obj.Reason != ""
	}
	return obj, false
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
