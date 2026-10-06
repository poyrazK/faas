package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BindingReleasePolicyStore = (*PgStore)(nil)

func decodeBindingReleasePolicy(body []byte, appID, scope string) (api.BindingReleasePolicy, error) {
	p := defaultBindingReleasePolicy(appID, scope)
	var row struct {
		Mode                  string     `json:"mode"`
		Revision              int64      `json:"revision"`
		MaxAgeSeconds         int64      `json:"max_age_seconds"`
		RequireApplicationAck bool       `json:"require_application_ack"`
		UpdatedAt             *time.Time `json:"updated_at"`
		Reason                string     `json:"reason"`
	}
	if err := json.Unmarshal(body, &row); err != nil {
		return p, fmt.Errorf("decode binding release policy: %w", err)
	}
	if row.Revision > 0 {
		p.Mode, p.Revision, p.MaxVerificationAge, p.RequireApplicationAck, p.UpdatedAt, p.Reason = row.Mode, row.Revision, (time.Duration(row.MaxAgeSeconds) * time.Second).String(), row.RequireApplicationAck, row.UpdatedAt, row.Reason
	}
	return p, nil
}
func (s *PgStore) GetBindingReleasePolicy(ctx context.Context, accountID, appID, scope string) (api.BindingReleasePolicy, error) {
	body, err := sqlc.New().ReadBindingReleasePolicy(ctx, s.pool, sqlc.ReadBindingReleasePolicyParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: normalizedDeploymentScope(scope)})
	if err != nil {
		return api.BindingReleasePolicy{}, mapErr(err)
	}
	return decodeBindingReleasePolicy(body, appID, scope)
}
func (s *PgStore) SetBindingReleasePolicy(ctx context.Context, accountID, appID, scope string, r api.SetBindingReleasePolicyRequest) (api.BindingReleasePolicy, error) {
	if api.ValidateBindingReleasePolicyRequest(r) != nil || api.ValidateScope(normalizedDeploymentScope(scope)) != nil {
		return api.BindingReleasePolicy{}, ErrInvalidArgument
	}
	current, err := s.GetBindingReleasePolicy(ctx, accountID, appID, scope)
	if err != nil {
		return current, err
	}
	if current.Revision != *r.ExpectedRevision {
		return current, ErrBindingReleasePolicyRevision
	}
	age := api.DefaultBindingVerificationAge
	if r.MaxVerificationAge != "" {
		age, _ = time.ParseDuration(r.MaxVerificationAge)
	}
	q := sqlc.New()
	var body []byte
	if *r.ExpectedRevision == 0 {
		body, err = q.WriteBindingReleasePolicy(ctx, s.pool, sqlc.WriteBindingReleasePolicyParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: normalizedDeploymentScope(scope), Mode: r.Mode, ExpectedRevision: *r.ExpectedRevision, MaxAgeSeconds: int64(age / time.Second), RequireApplicationAck: r.RequireApplicationAck, Reason: r.Reason})
	} else {
		body, err = q.UpdateBindingReleasePolicy(ctx, s.pool, sqlc.UpdateBindingReleasePolicyParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: normalizedDeploymentScope(scope), Mode: r.Mode, ExpectedRevision: *r.ExpectedRevision, MaxAgeSeconds: int64(age / time.Second), RequireApplicationAck: r.RequireApplicationAck, Reason: r.Reason})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return current, ErrBindingReleasePolicyRevision
	}
	if err != nil {
		return current, fmt.Errorf("write binding release policy: %w", mapErr(err))
	}
	return decodeBindingReleasePolicy(body, appID, scope)
}

func pgAuthorizeBindingRelease(ctx context.Context, tx pgx.Tx) error {
	fences := bindingReleaseFences(ctx)
	if len(fences) == 0 {
		return nil
	}
	body, err := bindingReleaseGrantJSON(fences)
	if err != nil {
		return err
	}
	_, err = sqlc.New().AuthorizeBindingReleaseTraffic(ctx, tx, body)
	return mapErr(err)
}

func bindingReleaseGrantJSON(fences []BindingPromotionFence) ([]byte, error) {
	type grant struct {
		AccountID             string     `json:"account_id"`
		AppID                 string     `json:"app_id"`
		DeploymentID          string     `json:"deployment_id"`
		Scope                 string     `json:"scope"`
		Revision              string     `json:"revision"`
		ValidUntil            *time.Time `json:"valid_until"`
		PolicyRevision        int64      `json:"policy_revision"`
		MaxAgeSeconds         float64    `json:"max_age_seconds"`
		AllowUnsupported      bool       `json:"allow_unsupported"`
		RequireApplicationAck bool       `json:"require_application_ack"`
	}
	grants := make([]grant, 0, len(fences))
	for _, f := range fences {
		g := grant{AccountID: f.AccountID, AppID: f.AppID, DeploymentID: f.DeploymentID, Scope: f.Scope, Revision: f.Revision, PolicyRevision: f.PolicyRevision, MaxAgeSeconds: f.MaxVerificationAge.Seconds(), AllowUnsupported: f.AllowUnsupported, RequireApplicationAck: f.RequireApplicationAck}
		if !f.ValidUntil.IsZero() {
			stamp := f.ValidUntil
			g.ValidUntil = &stamp
		}
		grants = append(grants, g)
	}
	body, err := json.Marshal(grants)
	if err != nil {
		return nil, fmt.Errorf("encode binding release fences: %w", err)
	}
	return body, nil
}
