package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

// Approval is immutable; revocation retains the original approval and reason.
type ApplicationStandardException struct {
	appstandards.Exception
	OrgID      string     `json:"org_id"`
	AppID      string     `json:"app_id"`
	ApprovedBy string     `json:"approved_by"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedBy  string     `json:"revoked_by,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type ApplicationStandardExceptionRequest struct {
	ExpectedRevision int64              `json:"expected_revision"`
	StandardID       string             `json:"standard_id"`
	Version          int64              `json:"version"`
	Field            appstandards.Field `json:"field"`
	Value            json.RawMessage    `json:"value"`
	Reason           string             `json:"reason"`
	ExpiresAt        time.Time          `json:"expires_at"`
}

// These mutations remain private until the public activation gates pass.
type ApplicationStandardExceptionStore interface {
	ApproveApplicationStandardException(context.Context, string, string, string, ApplicationStandardExceptionRequest) (ApplicationStandardException, error)
	RevokeApplicationStandardException(context.Context, string, string, string, string, int64) (ApplicationStandardException, error)
	ListApplicationStandardExceptions(context.Context, string, string, string) ([]ApplicationStandardException, error)
}

func prepareStandardException(orgID, actorID, appID string, r ApplicationStandardExceptionRequest) (ApplicationStandardException, error) {
	var x ApplicationStandardException
	if !standardApprovalIdentityValid(orgID, actorID, appID) || !validStandardResourceRead(orgID, r.StandardID) || r.ExpectedRevision <= 0 || r.ExpectedRevision >= api.ApplicationStandardMaxVersion || r.Version <= 0 || r.Version > api.ApplicationStandardMaxVersion {
		return x, ErrInvalidArgument
	}
	reason := strings.TrimSpace(r.Reason)
	if !utf8.ValidString(reason) || reason == "" || len(reason) > api.ApplicationStandardMaxDescriptionBytes || r.ExpiresAt.IsZero() {
		return x, ErrInvalidArgument
	}
	raw, err := json.Marshal(appstandards.Settings{r.Field: r.Value})
	if err != nil {
		return x, ErrInvalidArgument
	}
	settings, err := appstandards.ParseSettings(raw, api.ApplicationStandardResolverLimits())
	if err != nil {
		return x, fmt.Errorf("invalid exception value: %w: %w", err, ErrInvalidArgument)
	}
	x = ApplicationStandardException{Exception: appstandards.Exception{ID: uuid.NewString(), StandardID: canonicalStandardUUID(r.StandardID), Version: r.Version, Field: r.Field, Value: settings[r.Field], Reason: reason, ExpiresAt: r.ExpiresAt.UTC().Truncate(time.Microsecond)}, OrgID: canonicalStandardUUID(orgID), AppID: canonicalStandardUUID(appID), ApprovedBy: canonicalStandardUUID(actorID)}
	return x, nil
}

func validateStandardException(s standardReviewSnapshot, before ApplicationStandardEnrollment, x ApplicationStandardException, expected int64, now time.Time) error {
	if before.DesiredRevision != expected {
		return ErrApplicationStandardLocalIntentStale
	}
	if before.State != "blocked" && ((before.State != "persisted" && before.State != "observed") || before.PersistedRevision != before.DesiredRevision) {
		return ErrApplicationStandardsPending
	}
	if !x.ExpiresAt.After(now) || x.ExpiresAt.After(now.Add(api.ApplicationStandardMaxExceptionTTL)) {
		return ErrInvalidArgument
	}
	if !s.ScopeOwned || len(s.Applications) != 1 || !sameStandardUUID(s.Applications[0].AppID, x.AppID) {
		return ErrNotFound
	}
	app := &s.Applications[0]
	if len(app.Exceptions) >= api.ApplicationStandardMaxActiveExceptions {
		return ErrInvalidArgument
	}
	for _, existing := range app.Exceptions {
		if existing.RevokedAt == nil && existing.ExpiresAt.After(now) && existing.StandardID == x.StandardID && existing.Version == x.Version && existing.Field == x.Field {
			return ErrConflict
		}
	}
	if !standardExceptionAdopted(s, *app, x) {
		return ErrInvalidArgument
	}
	app.Exceptions = append(append([]ApplicationStandardException{}, app.Exceptions...), x)
	_, _, code := resolveAutomaticStandardEnrollment(s, now)
	if code != "" {
		return standardLocalIntentRefusal(code)
	}
	return nil
}

func standardExceptionAdopted(s standardReviewSnapshot, app standardReviewAppSnapshot, x ApplicationStandardException) bool {
	for _, pin := range app.Enrollment.Adoptions {
		for _, assignment := range s.Assignments {
			if assignment.ID != pin.AssignmentID || assignment.StandardID != x.StandardID || pin.Version != x.Version {
				continue
			}
			for _, v := range s.Versions {
				if v.StandardID == x.StandardID && v.Version == x.Version {
					var d appstandards.Definition
					if json.Unmarshal(v.Definition, &d) != nil {
						return false
					}
					_, found := d[x.Field]
					return found
				}
			}
		}
	}
	return false
}

func standardResolverExceptions(records []ApplicationStandardException, now time.Time) []appstandards.Exception {
	xs := []appstandards.Exception{}
	for _, x := range records {
		if x.RevokedAt == nil && x.ExpiresAt.After(now) {
			xs = append(xs, x.Exception)
		}
	}
	return xs
}

func standardEffectiveExceptionExpiry(e appstandards.Effective, records []ApplicationStandardException) *time.Time {
	var deadline *time.Time
	for _, sources := range e.Sources {
		for _, source := range sources {
			for _, x := range records {
				if source.ExceptionID == x.ID && (deadline == nil || x.ExpiresAt.Before(*deadline)) {
					t := x.ExpiresAt
					deadline = &t
				}
			}
		}
	}
	return deadline
}

func standardExceptionAudit(x ApplicationStandardException, before ApplicationStandardEnrollment, actorID, action string, now time.Time) AuditLog {
	valueHash := standardReviewBytesDigest(x.Value)
	reasonHash := standardReviewBytesDigest([]byte(x.Reason))
	data, _ := json.Marshal(map[string]any{"org_id": x.OrgID, "app_id": x.AppID, "exception_id": x.ID, "standard_id": x.StandardID, "version": x.Version, "field": x.Field, "actor_id": canonicalStandardUUID(actorID), "expires_at": x.ExpiresAt, "value_hash": valueHash, "reason_hash": reasonHash, "previous_revision": before.DesiredRevision, "desired_revision": before.DesiredRevision + 1})
	return AuditLog{ID: uuid.New(), Kind: "application_standard.exception_" + action, ReceivedAt: now, Data: data}
}

func normalizeStandardExceptions(app *standardReviewAppSnapshot) error {
	if len(app.Exceptions) > api.ApplicationStandardMaxActiveExceptions {
		return ErrInvalidArgument
	}
	for i := range app.Exceptions {
		x := &app.Exceptions[i]
		x.CreatedAt, x.ExpiresAt = x.CreatedAt.UTC(), x.ExpiresAt.UTC()
		if !standardApprovalIdentityValid(x.OrgID, x.AppID, x.ID) || !sameStandardUUID(x.OrgID, app.OrgID) || !sameStandardUUID(x.AppID, app.AppID) || !validStandardResourceRead(x.OrgID, x.ApprovedBy) || x.RevokedAt != nil || !x.ExpiresAt.After(x.CreatedAt) {
			return ErrInvalidArgument
		}
	}
	slices.SortFunc(app.Exceptions, func(a, b ApplicationStandardException) int { return strings.Compare(a.ID, b.ID) })
	return nil
}

func cloneStandardException(x ApplicationStandardException) ApplicationStandardException {
	raw, _ := json.Marshal(x)
	var out ApplicationStandardException
	_ = json.Unmarshal(raw, &out)
	return out
}
