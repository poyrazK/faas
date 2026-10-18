// Package regressiontriage validates exact debugger workflow transitions.
package regressiontriage

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func Selection(deployment, route string) (pgtype.UUID, *api.Problem) {
	id, err := uuid.Parse(strings.TrimSpace(deployment))
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}, api.ErrValidation("deployment_id must be a nonzero UUID")
	}
	if strings.TrimSpace(route) == "" || len(route) > 256 || !utf8.ValidString(route) {
		return pgtype.UUID{}, api.ErrValidation("route must contain 1 to 256 UTF-8 bytes")
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

// Prepare retains the legacy optional-guard API while requiring complete,
// exact app/version bindings and a reason for guarded management actions.
func Prepare(req api.GuardedDebugRegressionActionRequest, appID string, now time.Time) (sqlc.ApplyRegressionActionParams, *api.Problem) {
	return prepare(req, appID, now, true)
}

// PrepareChecked defers relative-time validation until after the atomic receipt
// lookup. An already-committed dismissal remains replayable after its expiry.
func PrepareChecked(req api.GuardedDebugRegressionActionRequest, appID string, now time.Time) (sqlc.ApplyRegressionActionParams, *api.Problem) {
	return prepare(req, appID, now, false)
}

func prepare(req api.GuardedDebugRegressionActionRequest, appID string, now time.Time, checkExpiry bool) (sqlc.ApplyRegressionActionParams, *api.Problem) {
	out := sqlc.ApplyRegressionActionParams{AppID: pgtype.UUID{Bytes: uuid.MustParse(appID), Valid: true}, Route: strings.TrimSpace(req.Route)}
	var problem *api.Problem
	out.DeploymentID, problem = Selection(req.DeploymentID, out.Route)
	if problem != nil {
		return out, problem
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if !api.AllowedDebugRegressionAction(action) {
		return out, api.ErrValidation("action must be acknowledge, dismiss, resolve, or reopen")
	}
	out.State = map[string]string{"acknowledge": "acknowledged", "dismiss": "dismissed", "resolve": "resolved", "reopen": "active"}[action]
	if req.ExpectedAppID != "" || req.ExpectedVersion != "" {
		version, err := uuid.Parse(req.ExpectedVersion)
		if err != nil || version == uuid.Nil || version.String() != req.ExpectedVersion || req.ExpectedAppID == "" {
			return out, api.ErrValidation("expected_app_id and a canonical nonzero expected_version are required together")
		}
		if req.ExpectedAppID != appID {
			return out, api.NewProblem(409, api.CodeRegressionChanged, "Regression changed", "reviewed app identity changed")
		}
		if req.Route != out.Route {
			return out, api.ErrValidation("guarded route must match the exact reviewed route")
		}
		out.ExpectedVersion = pgtype.UUID{Bytes: version, Valid: true}
		if strings.TrimSpace(req.Reason) == "" {
			return out, api.ErrValidation("guarded actions require a review reason")
		}
	}
	if !utf8.ValidString(req.Reason) || utf8.RuneCountInString(req.Reason) > 280 {
		return out, api.ErrValidation("reason must be valid UTF-8 and at most 280 characters")
	}
	if action == "dismiss" {
		until := now.Add(api.DebugRegressionDefaultDismissal)
		if strings.TrimSpace(req.DismissedUntil) != "" {
			var err error
			until, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(req.DismissedUntil))
			if err != nil {
				return out, api.ErrValidation("dismissed_until must be an RFC3339 timestamp")
			}
		}
		if checkExpiry && (!until.After(now) || until.After(now.Add(api.DebugRegressionMaxDismissal))) {
			return out, api.ErrValidation("dismissed_until must be in the future and no more than 30 days away")
		}
		out.Column5 = pgtype.Timestamptz{Time: until.UTC(), Valid: true}
	} else if strings.TrimSpace(req.DismissedUntil) != "" {
		return out, api.ErrValidation("dismissed_until is only valid for dismiss")
	}
	return out, nil
}
