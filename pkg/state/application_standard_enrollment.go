package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/appstandards"
)

var ErrApplicationStandardsPending = errors.New("state: application standards enrollment is pending")

// This is the durable control-plane projection. ObservedRevision is advanced
// only from actual consumer evidence, never by persisting configuration.
type ApplicationStandardEnrollment struct {
	AppID                     string                  `json:"app_id"`
	OrgID                     string                  `json:"org_id"`
	ProjectID                 string                  `json:"project_id,omitempty"`
	BaseSettings              appstandards.Settings   `json:"base_settings"`
	LocalSettings             appstandards.Settings   `json:"local_settings"`
	AdditionalLogDestinations []string                `json:"additional_log_destinations"`
	Adoptions                 []appstandards.Adoption `json:"adoptions"`
	Effective                 appstandards.Effective  `json:"effective"`
	MaterializedFields        []appstandards.Field    `json:"materialized_fields"`
	EffectiveHash             string                  `json:"effective_hash"`
	DesiredRevision           int64                   `json:"desired_revision"`
	PersistedRevision         int64                   `json:"persisted_revision"`
	ObservedRevision          int64                   `json:"observed_revision"`
	State                     string                  `json:"state"`
	ErrorCode                 string                  `json:"error_code,omitempty"`
	UpdatedAt                 time.Time               `json:"updated_at"`
}

type ApplicationStandardEnrollmentStore interface {
	GetApplicationStandardEnrollment(context.Context, string, string) (ApplicationStandardEnrollment, error)
	ListApplicationStandardAssignments(context.Context, string) ([]appstandards.Assignment, error)
}

// ApplicationStandardEnrollmentPermitsRuntime checks the persisted admission
// envelope, not consumer convergence. Runtime may be needed to obtain actual
// observations, so ObservedRevision is deliberately not a prerequisite here.
func ApplicationStandardEnrollmentPermitsRuntime(app App, enrollment ApplicationStandardEnrollment) bool {
	if !sameStandardUUID(app.ID, enrollment.AppID) || !sameStandardUUID(app.OrgID, enrollment.OrgID) {
		return false
	}
	if app.ProjectID != "" || enrollment.ProjectID != "" {
		if !sameStandardUUID(app.ProjectID, enrollment.ProjectID) {
			return false
		}
	}
	if enrollment.DesiredRevision <= 0 {
		return false
	}
	switch enrollment.State {
	case "unmanaged":
		return len(enrollment.Adoptions) == 0 && len(enrollment.MaterializedFields) == 0
	case "persisted", "observed":
		return enrollment.PersistedRevision == enrollment.DesiredRevision && enrollment.EffectiveHash != ""
	default:
		return false
	}
}

func applicationStandardBaseSettings(app App) appstandards.Settings {
	cidrs := []string{}
	for _, prefix := range app.EgressAllowlist {
		cidrs = append(cidrs, prefix.String())
	}
	ports := append([]int{}, app.EgressPorts...)
	policy := app.SecurityPolicy
	if !policy.Valid() {
		policy = "off"
	}
	signed, _ := json.Marshal(app.RequireSigned)
	posture, _ := json.Marshal(policy)
	ranges, _ := json.Marshal(cidrs)
	extra, _ := json.Marshal(ports)
	return appstandards.Settings{appstandards.RequireSigned: signed, appstandards.SecurityPolicy: posture, appstandards.EgressCIDRs: ranges, appstandards.EgressExtraPorts: extra}
}

func cloneApplicationStandardEnrollment(in ApplicationStandardEnrollment) ApplicationStandardEnrollment {
	raw, _ := json.Marshal(in)
	var out ApplicationStandardEnrollment
	_ = json.Unmarshal(raw, &out)
	return out
}
