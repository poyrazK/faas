package api

import "time"

// FlagSDKTrackCapability marks a runtime SDK that reports conversion events
// with track() (ADR 20261115163113 §2).
const FlagSDKTrackCapability = "track-v1"

// FlagEventsMaxBodyBytes bounds one runtime event batch body.
const FlagEventsMaxBodyBytes = 64 << 10

// FlagEventMaxAge and FlagEventMaxSkew bound an event's time against the
// API's clock: older events are dropped as invalid, and so are events more
// than FlagEventMaxSkew in the future.
const (
	FlagEventMaxAge  = 24 * time.Hour
	FlagEventMaxSkew = 5 * time.Minute
	// FlagEventMaxAbsValue bounds an event value (an integer, such as cents).
	FlagEventMaxAbsValue int64 = 1_000_000_000_000
)

// FlagEvent is one conversion event in a runtime batch. CustomerID is the
// verified customer the gateway attached to the request the event belongs
// to; apid keeps it only when it is an active platform tenant of the
// workload's account.
type FlagEvent struct {
	Name       string    `json:"name"`
	CustomerID string    `json:"customer_id"`
	Value      int64     `json:"value,omitempty"`
	At         time.Time `json:"at,omitempty"`
}

// FlagEventsRequest is POST /v1/runtime/flags/events.
type FlagEventsRequest struct {
	Events []FlagEvent `json:"events"`
}

// FlagEventsDropped counts a batch's dropped events by reason.
type FlagEventsDropped struct {
	Quota           int `json:"quota"`
	UnknownCustomer int `json:"unknown_customer"`
	NameLimit       int `json:"name_limit"`
	Invalid         int `json:"invalid"`
}

// FlagEventsMonth is the account's conversion event usage this month.
type FlagEventsMonth struct {
	Limit    int64 `json:"limit"`
	Accepted int64 `json:"accepted"`
	Dropped  int64 `json:"dropped"`
}

// FlagEventsResponse answers a runtime event batch.
type FlagEventsResponse struct {
	Accepted int               `json:"accepted"`
	Dropped  FlagEventsDropped `json:"dropped"`
	Month    FlagEventsMonth   `json:"month"`
}
