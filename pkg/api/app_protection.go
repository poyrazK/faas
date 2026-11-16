package api

import "context"

// App protection enrollment as GET /v1/apps/{slug}/protection reports it
// (ADR-20261116120000). none: the app was never enrolled.
const (
	AppProtectionStateOn   = "on"
	AppProtectionStateOff  = "off"
	AppProtectionStateNone = "none"
)

// Protection baseline item states. active: the baseline created the object
// and it still exists; pending: not created yet (the app has no live
// production deployment, or the reconciler has not run); skipped: it could
// not be created yet, Reason says why, and it is retried; removed: the
// baseline created it and the customer deleted it, so it is not recreated.
const (
	AppProtectionItemActive  = "active"
	AppProtectionItemPending = "pending"
	AppProtectionItemSkipped = "skipped"
	AppProtectionItemRemoved = "removed"
)

// Why a baseline item was skipped.
const (
	// The account's plan does not include the object's feature.
	AppProtectionSkipPlan = "plan"
	// The feature is not released to the account.
	AppProtectionSkipNotReleased = "not_released"
	// Email alerts need the account's email address verified.
	AppProtectionSkipEmailUnverified = "email_unverified"
	// The customer already has an object with the baseline's name.
	AppProtectionSkipNameTaken = "name_taken"
	// The app or account is at its limit for this kind of object.
	AppProtectionSkipLimitReached = "limit_reached"
	// The item needs the SLO it watches, which was not created.
	AppProtectionSkipNeedsSLO = "needs_slo"
	// The alert needs the email channel, which was not created.
	AppProtectionSkipNeedsChannel = "needs_channel"
	// The health alert needs the health check, which was not created.
	AppProtectionSkipNeedsCheck = "needs_check"
	// The platform's unbilled health probes are not running on this
	// deployment, so a baseline health check would never run.
	AppProtectionSkipProbesUnavailable = "probes_unavailable"
)

// ProtectionBaselineItem describes one object of the protection baseline:
// its stable item key, the kind of object it is, and the name it is created
// under.
type ProtectionBaselineItem struct {
	Item string
	Kind string
	Name string
}

// ProtectionBaselineItems is the baseline in status order. Item keys match
// app_protection_objects.item.
var ProtectionBaselineItems = []ProtectionBaselineItem{
	{Item: "availability_slo", Kind: "slo", Name: ProtectionBaselineAvailabilitySLOName},
	{Item: "latency_slo", Kind: "slo", Name: ProtectionBaselineLatencySLOName},
	{Item: "health_check", Kind: "synthetic_check", Name: ProtectionBaselineHealthCheckName},
	{Item: "email_channel", Kind: "notification_channel", Name: ProtectionBaselineEmailChannelName},
	{Item: "availability_burn_alert", Kind: "alert_rule", Name: ProtectionBaselineAvailabilityBurnAlertName},
	{Item: "latency_burn_alert", Kind: "alert_rule", Name: ProtectionBaselineLatencyBurnAlertName},
	{Item: "health_alert", Kind: "alert_rule", Name: ProtectionBaselineHealthAlertName},
	{Item: "availability_gate", Kind: "release_gate", Name: ProtectionBaselineAvailabilityGateName},
}

// AppProtectionResponse is GET /v1/apps/{slug}/protection.
type AppProtectionResponse struct {
	// State is on, off or none (never enrolled).
	State string `json:"state"`
	// Protected is true when the app is enrolled and every baseline item
	// is active.
	Protected bool `json:"protected"`
	// Items is every baseline item in a fixed order; empty when State is
	// none.
	Items []AppProtectionItemStatus `json:"items"`
}

// AppProtectionItemStatus is one baseline item's status.
type AppProtectionItemStatus struct {
	Item string `json:"item"`
	// Kind is slo, synthetic_check, notification_channel, alert_rule or
	// release_gate.
	Kind string `json:"kind"`
	// Name is the object's name.
	Name string `json:"name"`
	// State is active, pending, skipped or removed.
	State string `json:"state"`
	// ObjectID identifies the created object when State is active or
	// removed.
	ObjectID string `json:"object_id,omitempty"`
	// Reason says why a skipped item could not be created yet.
	Reason string `json:"reason,omitempty"`
}

// GetAppProtection reports an app's protection baseline
// (ADR-20261116120000).
func (c *Client) GetAppProtection(ctx context.Context, slug string) (AppProtectionResponse, error) {
	var out AppProtectionResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/protection", nil, &out)
}
