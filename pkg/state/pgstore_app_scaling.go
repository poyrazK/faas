// adr: 20261104104521
package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) AppScaling(ctx context.Context, accountID, appID, scope string) (api.AppScalingSnapshot, error) {
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return api.AppScalingSnapshot{}, err
	}
	if app.AccountID != accountID || app.Status == AppDeleted {
		return api.AppScalingSnapshot{}, ErrNotFound
	}
	revision := app.ScalingRevision
	var environmentID string
	if scope != "default" {
		spec, err := s.ProjectEnvironmentWorkloadSpec(ctx, accountID, app.ProjectID, scope, appID)
		if err != nil {
			return api.AppScalingSnapshot{}, err
		}
		app, err = spec.Settings.ApplyTo(app)
		if err != nil {
			return api.AppScalingSnapshot{}, err
		}
		revision = spec.Revision
		environmentID = spec.EnvironmentID
	}
	return api.AppScalingSnapshot{AppID: appID, Scope: scope, EnvironmentID: environmentID, Revision: revision, Settings: scalingSettings(app)}, nil
}

func (s *PgStore) AppScalingReceipt(ctx context.Context, accountID, appID, scope, requestID string) (api.AppScalingReceipt, error) {
	// Current ownership/environment existence is required even for old receipts.
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	if app.AccountID != accountID || app.Status == AppDeleted {
		return api.AppScalingReceipt{}, ErrNotFound
	}
	var environmentID string
	if scope != "default" {
		env, err := s.ProjectEnvironmentBySlug(ctx, accountID, app.ProjectID, scope)
		if err != nil {
			return api.AppScalingReceipt{}, err
		}
		environmentID = env.ID
	}
	row, err := sqlc.New().ReadAppScalingReceipt(ctx, s.pool, sqlc.ReadAppScalingReceiptParams{AppID: mustPgUUID(appID), Scope: scope, RequestID: mustPgUUID(requestID)})
	if err != nil {
		return api.AppScalingReceipt{}, mapErr(err)
	}
	var out api.AppScalingReceipt
	err = json.Unmarshal(row.Response, &out)
	if err == nil && out.EnvironmentID != environmentID {
		return api.AppScalingReceipt{}, ErrNotFound
	}
	return out, err
}

func (s *PgStore) ApplyAppScaling(ctx context.Context, in AppScalingChange) (api.AppScalingReceipt, error) {
	hash, err := validateScalingChange(&in)
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	for attempt := 0; ; attempt++ {
		out, err := s.applyAppScalingTx(ctx, in, hash)
		var pgErr *pgconn.PgError
		if attempt >= 2 || !errors.As(err, &pgErr) || (pgErr.Code != "40P01" && pgErr.Code != "40001") {
			return out, err
		}
		if err := ctx.Err(); err != nil {
			return api.AppScalingReceipt{}, err
		}
	}
}

func (s *PgStore) applyAppScalingTx(ctx context.Context, in AppScalingChange, hash [32]byte) (api.AppScalingReceipt, error) {
	tx, err := s.beginAppConfigMutation(ctx, in.AppID, scalingParams(in.Request))
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	app, err := readAppForConfigUpdateTx(ctx, tx, in.AppID)
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	if app.AccountID != in.AccountID || app.Status == AppDeleted {
		return api.AppScalingReceipt{}, ErrNotFound
	}
	q := sqlc.New()
	var environmentID string
	var settings ProjectEnvironmentWorkloadSettings
	revision := app.ScalingRevision
	if in.Scope != "default" {
		env, err := q.LockAppScalingEnvironment(ctx, tx, sqlc.LockAppScalingEnvironmentParams{ID: mustPgUUID(app.ID), AccountID: mustPgUUID(in.AccountID), Slug: in.Scope})
		if err != nil {
			return api.AppScalingReceipt{}, mapErr(err)
		}
		if env.Protected {
			return api.AppScalingReceipt{}, ErrConflict
		}
		environmentID = uuid.UUID(env.ID.Bytes).String()
		if !scalingIdentityMatches(environmentID, in.Request.ExpectedEnvironmentID) {
			return api.AppScalingReceipt{}, ErrConflict
		}
	}
	previous, err := q.ReadAppScalingReceipt(ctx, tx, sqlc.ReadAppScalingReceiptParams{AppID: mustPgUUID(app.ID), Scope: in.Scope, RequestID: mustPgUUID(in.Request.RequestID)})
	if err == nil {
		if !bytes.Equal(previous.RequestHash, hash[:]) {
			return api.AppScalingReceipt{}, ErrScalingRequestConflict
		}
		var out api.AppScalingReceipt
		err = json.Unmarshal(previous.Response, &out)
		return out, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.AppScalingReceipt{}, err
	}
	if in.Scope != "default" {
		row, err := q.ReadAppScalingWorkload(ctx, tx, sqlc.ReadAppScalingWorkloadParams{AppID: mustPgUUID(app.ID), Slug: in.Scope})
		if err != nil {
			return api.AppScalingReceipt{}, mapErr(err)
		}
		if err := json.Unmarshal(row.Settings, &settings); err != nil {
			return api.AppScalingReceipt{}, err
		}
		settingsHash, err := WorkloadSettingsHash(settings)
		if err != nil || settingsHash != row.ConfigHash {
			return api.AppScalingReceipt{}, ErrConflict
		}
		app, err = settings.ApplyTo(app)
		if err != nil {
			return api.AppScalingReceipt{}, err
		}
		revision = row.Revision
	}
	if revision != *in.Request.ExpectedRevision {
		return api.AppScalingReceipt{}, ErrScalingRevisionConflict
	}
	if in.Validate != nil {
		if err := in.Validate(app); err != nil {
			return api.AppScalingReceipt{}, err
		}
	}
	params := reviewedScalingParams(app, in.Request)
	var after App
	var nextRevision int64
	if in.Scope == "default" {
		after, err = updateApp(ctx, tx, app.ID, params)
		if err == nil {
			after, err = syncProductionWorkloadSpecTx(ctx, tx, after, params)
		}
		if err == nil {
			nextRevision, err = q.AdvanceAppScalingRevision(ctx, tx, sqlc.AdvanceAppScalingRevisionParams{ID: mustPgUUID(app.ID), Column2: revision})
		}
	} else {
		var spec ProjectEnvironmentWorkloadSpec
		settings, err = ApplyWorkloadSettingsUpdate(settings, params)
		if err == nil {
			spec, err = putWorkloadSpecTx(ctx, tx, environmentID, app.ID, revision, settings)
		}
		if err == nil {
			after, err = spec.Settings.ApplyTo(app)
			nextRevision = spec.Revision
		}
	}
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	out := api.AppScalingReceipt{AppScalingSnapshot: api.AppScalingSnapshot{AppID: app.ID, Scope: in.Scope, EnvironmentID: environmentID, Revision: nextRevision, Settings: scalingSettings(after)}, RequestID: in.Request.RequestID, PreviousRevision: revision, CommittedAt: time.Now().UTC()}
	raw, err := json.Marshal(out)
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	err = q.InsertAppScalingReceipt(ctx, tx, sqlc.InsertAppScalingReceiptParams{AppID: mustPgUUID(app.ID), Scope: in.Scope, RequestID: mustPgUUID(in.Request.RequestID), RequestHash: hash[:], Response: raw})
	if err != nil {
		return api.AppScalingReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.AppScalingReceipt{}, err
	}
	return out, nil
}
