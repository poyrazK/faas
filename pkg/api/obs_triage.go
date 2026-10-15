package api

import "time"

// ObsAlertTriage describes human workflow independently of observed alert status.
// OwnerAccountID is an operator identity; diagnostic bundles remove it.
type ObsAlertTriage struct {
	Revision       int64      `json:"revision"`
	Acknowledged   bool       `json:"acknowledged"`
	Owned          bool       `json:"owned"`
	OwnerAccountID string     `json:"owner_account_id,omitempty"`
	SnoozedUntil   *time.Time `json:"snoozed_until,omitempty"`
	Snoozed        bool       `json:"snoozed"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
	AsOf           time.Time  `json:"as_of"`
}

type ObsAlertTriageRequest struct {
	Action           string     `json:"action"`
	ExpectedRevision *int64     `json:"expected_revision"`
	SnoozedUntil     *time.Time `json:"snoozed_until,omitempty"`
}

type ObsAlertTriageResponse struct {
	IncidentID string         `json:"incident_id"`
	Triage     ObsAlertTriage `json:"triage"`
}

func AllowedObsAlertTriageAction(action string) bool {
	switch action {
	case "acknowledge", "unacknowledge", "claim", "release", "snooze", "unsnooze":
		return true
	}
	return false
}
