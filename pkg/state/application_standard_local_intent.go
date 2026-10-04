package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

var ErrApplicationStandardLocalIntentStale = errors.New("state: application standard local intent changed")

type ApplicationStandardLocalIntentRequest struct {
	ExpectedRevision          int64           `json:"expected_revision"`
	Settings                  json.RawMessage `json:"settings"`
	AdditionalLogDestinations []string        `json:"additional_log_destinations"`
}

// Saving permitted local intent does not bypass materialization or observation.
// This interface remains private alongside reviewed activation.
type ApplicationStandardLocalIntentStore interface {
	SetApplicationStandardLocalIntent(context.Context, string, string, string, ApplicationStandardLocalIntentRequest) (ApplicationStandardEnrollment, error)
}

type standardLocalIntent struct {
	ExpectedRevision int64
	Settings         appstandards.Settings
	Additional       []string
}

func prepareStandardLocalIntent(orgID, actorID, appID string, r ApplicationStandardLocalIntentRequest) (standardLocalIntent, error) {
	var intent standardLocalIntent
	if !standardApprovalIdentityValid(orgID, actorID, appID) || r.ExpectedRevision <= 0 || r.ExpectedRevision >= api.ApplicationStandardMaxVersion || len(r.AdditionalLogDestinations) > api.ApplicationStandardResolverLimits().SetEntries {
		return intent, ErrInvalidArgument
	}
	settings, err := appstandards.ParseSettings(r.Settings, api.ApplicationStandardResolverLimits())
	if err != nil {
		return intent, fmt.Errorf("invalid local application settings: %w: %w", err, ErrInvalidArgument)
	}
	additional := append([]string{}, r.AdditionalLogDestinations...)
	raw, _ := json.Marshal(appstandards.Settings{appstandards.LogDestinations: mustStandardLocalJSON(additional)})
	normal, err := appstandards.ParseSettings(raw, api.ApplicationStandardResolverLimits())
	if err != nil {
		return intent, fmt.Errorf("invalid additional log destinations: %w: %w", err, ErrInvalidArgument)
	}
	return standardLocalIntent{ExpectedRevision: r.ExpectedRevision, Settings: settings, Additional: standardReviewStrings(normal[appstandards.LogDestinations])}, nil
}

func mustStandardLocalJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func validateStandardLocalIntent(s standardReviewSnapshot, current ApplicationStandardEnrollment, intent standardLocalIntent, now time.Time) (ApplicationStandardReviewedApp, bool, error) {
	var proposed ApplicationStandardReviewedApp
	if current.DesiredRevision != intent.ExpectedRevision {
		return proposed, false, ErrApplicationStandardLocalIntentStale
	}
	if current.State != "blocked" && ((current.State != "persisted" && current.State != "observed") || current.PersistedRevision != current.DesiredRevision || current.EffectiveHash == "") {
		return proposed, false, ErrApplicationStandardsPending
	}
	var err error
	s, err = normalizeStandardReviewSnapshot(s)
	if err != nil {
		return proposed, false, err
	}
	if len(s.Applications) != 1 || !s.Applications[0].HasEnrollment || !s.ScopeOwned || s.Applications[0].AppID != canonicalStandardUUID(current.AppID) {
		return proposed, false, ErrNotFound
	}
	app := s.Applications[0]
	if len(app.Enrollment.Adoptions) == 0 && len(app.Enrollment.MaterializedFields) == 0 {
		return proposed, false, ErrInvalidArgument
	}
	before, _ := standardReviewDigest(app.Enrollment.LocalSettings)
	after, _ := standardReviewDigest(intent.Settings)
	unchanged := before == after && slices.Equal(app.Enrollment.AdditionalLogDestinations, intent.Additional)
	s.Applications[0].Enrollment.LocalSettings = cloneStandardSettings(intent.Settings)
	s.Applications[0].Enrollment.AdditionalLogDestinations = []string{}
	_, baseline, code, err := resolveAutomaticStandardEnrollment(s, now)
	if err != nil || code != "" {
		return proposed, false, standardLocalIntentRefusal(code, err)
	}
	for field := range intent.Settings {
		if len(baseline.Effective.Sources[field]) == 0 {
			return proposed, false, fmt.Errorf("local standard intent needs an inherited field (%s): %w", field, ErrInvalidArgument)
		}
	}
	if err := validateStandardLocalLogIntent(baseline, intent); err != nil {
		return proposed, false, err
	}
	s.Applications[0].Enrollment.AdditionalLogDestinations = append([]string{}, intent.Additional...)
	_, proposed, code, err = resolveAutomaticStandardEnrollment(s, now)
	if err != nil || code != "" {
		return proposed, false, standardLocalIntentRefusal(code, err)
	}
	return proposed, !unchanged, nil
}

func standardLocalIntentRefusal(code string, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("local application intent refused (%s): %w", code, ErrApplicationStandardReviewBlocked)
}

func validateStandardLocalLogIntent(baseline ApplicationStandardReviewedApp, intent standardLocalIntent) error {
	sources := baseline.Effective.Sources[appstandards.LogDestinations]
	constrained := false
	for _, source := range sources {
		if source.Mode == appstandards.Default {
			continue
		}
		constrained = true
		if _, exists := intent.Settings[appstandards.LogDestinations]; exists {
			return fmt.Errorf("required logging uses additional destinations: %w", ErrInvalidArgument)
		}
		if len(intent.Additional) > 0 && (source.Mode != appstandards.Mandatory || source.Override != appstandards.Extend) {
			return standardLocalIntentRefusal("logging_extension_not_permitted", nil)
		}
	}
	if len(intent.Additional) > 0 && !constrained {
		return standardLocalIntentRefusal("logging_extension_not_permitted", nil)
	}
	for _, id := range intent.Additional {
		if slices.Contains(standardReviewStrings(baseline.Effective.Values[appstandards.LogDestinations]), id) {
			return fmt.Errorf("a required destination cannot be enrolled as an extra: %w", ErrInvalidArgument)
		}
	}
	return nil
}

func standardSavedLocalIntent(current ApplicationStandardEnrollment, proposed ApplicationStandardReviewedApp, now time.Time) ApplicationStandardEnrollment {
	e := cloneApplicationStandardEnrollment(current)
	e.LocalSettings, e.AdditionalLogDestinations = cloneStandardSettings(proposed.LocalSettings), append([]string{}, proposed.AdditionalLogDestinations...)
	e.DesiredRevision++
	e.State, e.ErrorCode, e.UpdatedAt = "pending", "", now.UTC().Truncate(time.Microsecond)
	return e // Retain the last installed projection and its actual observation.
}

func standardLocalIntentAudit(before, after ApplicationStandardEnrollment, proposed ApplicationStandardReviewedApp, actorID string) AuditLog {
	intentHash, _ := standardReviewDigest(after.LocalSettings)
	additionalHash, _ := standardReviewDigest(after.AdditionalLogDestinations)
	proposedHash, _ := standardReviewDigest(proposed.Effective)
	data, _ := json.Marshal(map[string]any{"org_id": canonicalStandardUUID(after.OrgID), "app_id": canonicalStandardUUID(after.AppID), "actor_id": canonicalStandardUUID(actorID), "previous_revision": before.DesiredRevision, "desired_revision": after.DesiredRevision, "local_intent_hash": intentHash, "additional_destinations_hash": additionalHash, "proposed_effective_hash": proposedHash})
	return AuditLog{ID: uuid.New(), Kind: "application_standard.local_intent_changed", ReceivedAt: after.UpdatedAt, Data: data}
}
