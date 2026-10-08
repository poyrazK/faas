package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func profileCheckTime(at time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: at, Valid: !at.IsZero()}
}

func decodeProfileCheck(body []byte) (api.ProfileDeploymentCheck, error) {
	var out api.ProfileDeploymentCheck
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("decode deployment profiling check: %w", err)
	}
	out.Config.Options = api.NormalizeProfileRegressionOptions(out.Config.Options)
	return out, nil
}

func readProfileDeploymentPolicy(ctx context.Context, db sqlc.DBTX, accountID, appID string) (api.ProfileDeploymentPolicy, error) {
	body, err := sqlc.New().ReadProfileDeploymentPolicy(ctx, db, sqlc.ReadProfileDeploymentPolicyParams{AppID: appID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ProfileDeploymentPolicy{AppID: appID}, nil
	}
	if err != nil {
		return api.ProfileDeploymentPolicy{}, fmt.Errorf("read automatic profiling policy: %w", err)
	}
	var out api.ProfileDeploymentPolicy
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("decode automatic profiling policy: %w", err)
	}
	out.Config.Options = api.NormalizeProfileRegressionOptions(out.Config.Options)
	return out, nil
}

func (s *PgStore) GetProfileDeploymentPolicy(ctx context.Context, accountID, appID string) (api.ProfileDeploymentPolicy, error) {
	app, err := s.AppByID(ctx, appID)
	if err != nil || app.AccountID != accountID || app.Status == AppDeleted {
		return api.ProfileDeploymentPolicy{}, ErrNotFound
	}
	p, err := readProfileDeploymentPolicy(ctx, s.pool, accountID, appID)
	if err == nil && p.Revision == 0 {
		p = DefaultProfileDeploymentPolicy(appID, app.Runtime)
	}
	return p, err
}

func (s *PgStore) SaveProfileDeploymentPolicy(ctx context.Context, accountID, appID string, req api.SaveProfileDeploymentPolicyRequest) (api.ProfileDeploymentPolicy, error) {
	if err := ValidateProfileDeploymentPolicy(req); err != nil {
		return api.ProfileDeploymentPolicy{}, err
	}
	req.Config.Options = api.NormalizeProfileRegressionOptions(req.Config.Options)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ProfileDeploymentPolicy{}, fmt.Errorf("begin automatic profiling policy: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return api.ProfileDeploymentPolicy{}, routePolicyReadError(err)
	}
	p, err := readProfileDeploymentPolicy(ctx, tx, accountID, appID)
	if err != nil {
		return p, err
	}
	if p.Revision != *req.ExpectedRevision {
		return p, ErrProfileInvestigationRevision
	}
	body, err := json.Marshal(req.Config)
	if err != nil {
		return p, fmt.Errorf("encode automatic profiling policy: %w", err)
	}
	if err := q.WriteProfileDeploymentPolicy(ctx, tx, sqlc.WriteProfileDeploymentPolicyParams{AppID: appID, AccountID: accountID, Revision: p.Revision + 1, Enabled: req.Config.Enabled, Config: body}); err != nil {
		return p, fmt.Errorf("write automatic profiling policy: %w", err)
	}
	if err := q.CancelProfileDeploymentChecks(ctx, tx, appID); err != nil {
		return p, fmt.Errorf("cancel superseded profiling checks: %w", err)
	}
	if err := q.CancelProfileCanaryChecks(ctx, tx, appID); err != nil {
		return p, fmt.Errorf("cancel superseded canary profiling checks: %w", err)
	}
	out, err := readProfileDeploymentPolicy(ctx, tx, accountID, appID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) ListProfileDeploymentChecks(ctx context.Context, accountID, appID string) ([]api.ProfileDeploymentCheck, error) {
	owned, err := sqlc.New().ProfileInvestigationAppOwned(ctx, s.pool, sqlc.ProfileInvestigationAppOwnedParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("read profiling check app: %w", err)
	}
	if !owned {
		return nil, ErrNotFound
	}
	bodies, err := sqlc.New().ListProfileDeploymentChecks(ctx, s.pool, sqlc.ListProfileDeploymentChecksParams{AppID: appID, AccountID: accountID, MaxRows: api.ProfileAutoMaxResults})
	if err != nil {
		return nil, fmt.Errorf("list deployment profiling checks: %w", err)
	}
	out := []api.ProfileDeploymentCheck{}
	for _, body := range bodies {
		row, err := decodeProfileCheck(body)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

func readProfileDeploymentCheck(ctx context.Context, db sqlc.DBTX, accountID, appID, id string) (api.ProfileDeploymentCheck, error) {
	body, err := sqlc.New().ReadProfileDeploymentCheck(ctx, db, sqlc.ReadProfileDeploymentCheckParams{AppID: appID, AccountID: accountID, DeploymentID: id})
	if err != nil {
		return api.ProfileDeploymentCheck{}, routePolicyReadError(err)
	}
	return decodeProfileCheck(body)
}

func (s *PgStore) GetProfileDeploymentCheck(ctx context.Context, accountID, appID, id string) (api.ProfileDeploymentCheck, error) {
	return readProfileDeploymentCheck(ctx, s.pool, accountID, appID, id)
}

type profileDeploymentCandidate struct {
	DeploymentID string                      `json:"deployment_id"`
	AppID        string                      `json:"app_id"`
	AccountID    string                      `json:"account_id"`
	Scope        string                      `json:"scope"`
	CreatedAt    time.Time                   `json:"created_at"`
	CompletedAt  time.Time                   `json:"completed_at"`
	Policy       api.ProfileDeploymentPolicy `json:"policy"`
	BaselineID   *string                     `json:"baseline_id"`
}

func (s *PgStore) DiscoverProfileDeploymentChecks(ctx context.Context, now time.Time) (int, error) {
	rows, err := sqlc.New().DiscoverProfileDeploymentCandidates(ctx, s.pool, sqlc.DiscoverProfileDeploymentCandidatesParams{Earliest: profileCheckTime(now.Add(-api.ProfileAutoDiscoveryLookback)), ObservedAt: profileCheckTime(now), BatchLimit: api.ProfileAutoBatchSize})
	if err != nil {
		return 0, fmt.Errorf("discover deployment profiling checks: %w", err)
	}
	total := 0
	for _, body := range rows {
		var candidate profileDeploymentCandidate
		if err := json.Unmarshal(body, &candidate); err != nil {
			return total, fmt.Errorf("decode profiling candidate: %w", err)
		}
		count, err := s.enqueueProfileDeploymentCheck(ctx, candidate, now)
		if err != nil {
			return total, err
		}
		total += count
	}
	return total, nil
}

func (s *PgStore) enqueueProfileDeploymentCheck(ctx context.Context, c profileDeploymentCandidate, now time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin deployment profiling check: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: c.AppID, AccountID: c.AccountID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("lock profiling check app: %w", err)
	}
	baseline := ""
	if c.BaselineID != nil {
		baseline = *c.BaselineID
	}
	check := newProfileDeploymentCheck(Deployment{ID: c.DeploymentID, AppID: c.AppID, Scope: c.Scope, CreatedAt: c.CreatedAt, RolloutCompletedAt: &c.CompletedAt}, baseline, c.Policy, now)
	body, err := json.Marshal(check)
	if err != nil {
		return 0, fmt.Errorf("encode deployment profiling check: %w", err)
	}
	count, err := q.EnqueueProfileDeploymentCheck(ctx, tx, sqlc.EnqueueProfileDeploymentCheckParams{DeploymentID: c.DeploymentID, AppID: c.AppID, AccountID: c.AccountID, PolicyRevision: c.Policy.Revision, Data: body, Due: profileCheckTime(*check.NextAttemptAt), ObservedAt: profileCheckTime(now)})
	if err != nil {
		return 0, fmt.Errorf("enqueue deployment profiling check: %w", err)
	}
	return int(count), tx.Commit(ctx)
}

func (s *PgStore) MaintainProfileDeploymentChecks(ctx context.Context, now time.Time) error {
	q := sqlc.New()
	if err := q.ExpireProfileDeploymentChecks(ctx, s.pool, sqlc.ExpireProfileDeploymentChecksParams{ObservedAt: profileCheckTime(now), MaxAttempts: api.ProfileAutoMaxAttempts}); err != nil {
		return fmt.Errorf("expire deployment profiling checks: %w", err)
	}
	if err := q.PruneProfileDeploymentChecks(ctx, s.pool, sqlc.PruneProfileDeploymentChecksParams{Cutoff: profileCheckTime(now.Add(-api.ProfileAutoReceiptRetention)), BatchLimit: api.ProfileAutoBatchSize}); err != nil {
		return fmt.Errorf("prune deployment profiling checks: %w", err)
	}
	return nil
}

func (s *PgStore) ClaimProfileDeploymentCheck(ctx context.Context, now time.Time) (ProfileDeploymentCheckWork, error) {
	token := uuid.NewString()
	row, err := sqlc.New().ClaimProfileDeploymentCheck(ctx, s.pool, sqlc.ClaimProfileDeploymentCheckParams{Token: token, ObservedAt: profileCheckTime(now), LeaseUntil: profileCheckTime(now.Add(api.ProfileAutoLeaseDuration)), MaxAttempts: api.ProfileAutoMaxAttempts})
	if err != nil {
		return ProfileDeploymentCheckWork{}, routePolicyReadError(err)
	}
	check, err := decodeProfileCheck(row.Receipt)
	return ProfileDeploymentCheckWork{Check: check, AccountID: row.AccountID, Token: token}, err
}

func (s *PgStore) FinishProfileDeploymentCheck(ctx context.Context, work ProfileDeploymentCheckWork, a api.ProfileRegressionAssessment, retry bool, now time.Time) (api.ProfileDeploymentCheck, error) {
	if err := validateProfileCheckResult(a); err != nil {
		return api.ProfileDeploymentCheck{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ProfileDeploymentCheck{}, fmt.Errorf("begin deployment profiling result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: work.Check.AppID, AccountID: work.AccountID}); err != nil {
		return api.ProfileDeploymentCheck{}, routePolicyReadError(err)
	}
	body, err := q.LockClaimedProfileDeploymentCheck(ctx, tx, sqlc.LockClaimedProfileDeploymentCheckParams{DeploymentID: work.Check.DeploymentID, AppID: work.Check.AppID, AccountID: work.AccountID, Token: work.Token, ObservedAt: profileCheckTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ProfileDeploymentCheck{}, ErrProfileCheckLease
	}
	if err != nil {
		return api.ProfileDeploymentCheck{}, fmt.Errorf("lock deployment profiling result: %w", err)
	}
	check, err := decodeProfileCheck(body)
	if err != nil {
		return check, err
	}
	observation := profileAlertObservation{AppID: check.AppID, AccountID: work.AccountID, Scope: check.Scope, Source: "deployment", Revision: check.PolicyRevision, Assessment: a}
	var alertPlans []profileAlertPlan
	if !(retry && check.Attempts < api.ProfileAutoMaxAttempts) {
		slug, err := q.ReadProfileAlertOwner(ctx, tx, sqlc.ReadProfileAlertOwnerParams{AppID: check.AppID, AccountID: work.AccountID})
		if err != nil {
			return check, err
		}
		observation.Slug = slug
		observation.EvidencePath = "/v1/apps/" + slug + "/profiles/deployment-checks/" + check.DeploymentID
		alertPlans, err = prepareProfileAlertsTx(ctx, tx, observation)
		if err != nil {
			return check, err
		}
	}
	status, reason := a.Status, a.Reason
	var due, completed pgtype.Timestamptz
	var investigation pgtype.Text
	if retry && check.Attempts < api.ProfileAutoMaxAttempts {
		status = "queued"
		due = profileCheckTime(now.Add(api.ProfileAutoRetryInterval))
	} else {
		completed = profileCheckTime(now)
		if check.Baseline != nil {
			id, err := saveAutoProfileInvestigationTx(ctx, tx, work.AccountID, check, a)
			if errors.Is(err, ErrProfileInvestigationQuota) {
				reason += " Investigation not saved: the app's saved-investigation limit is full."
			} else if err != nil {
				return check, err
			} else {
				investigation = pgtype.Text{String: id, Valid: true}
			}
		}
	}
	if err := q.FinishProfileDeploymentCheck(ctx, tx, sqlc.FinishProfileDeploymentCheckParams{DeploymentID: check.DeploymentID, Token: work.Token, Status: status, Reason: reason, NextAttemptAt: due, CompletedAt: completed, InvestigationID: investigation}); err != nil {
		return check, fmt.Errorf("finish deployment profiling check: %w", err)
	}
	out, err := readProfileDeploymentCheck(ctx, tx, work.AccountID, check.AppID, check.DeploymentID)
	if err != nil {
		return out, err
	}
	profileAlertInvestigation(alertPlans, observation.Slug, out.InvestigationID)
	if err := publishProfileAlertsTx(ctx, tx, observation, alertPlans); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func saveAutoProfileInvestigationTx(ctx context.Context, tx pgx.Tx, accountID string, check api.ProfileDeploymentCheck, a api.ProfileRegressionAssessment) (string, error) {
	q := sqlc.New()
	count, err := q.CountProfileInvestigations(ctx, tx, check.AppID)
	if err != nil {
		return "", fmt.Errorf("count automatic investigations: %w", err)
	}
	if count >= api.ProfileInvestigationMaxPerApp {
		return "", ErrProfileInvestigationQuota
	}
	req, err := profileCheckInvestigation(check, a)
	if err != nil {
		return "", err
	}
	current := api.ProfileInvestigation{Revision: 1, Investigation: req.Investigation}
	a.InvestigationRevision = 2
	assessment, err := validateProfileAssessment(current, 1, a)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(req.Investigation)
	if err != nil {
		return "", fmt.Errorf("encode automatic investigation: %w", err)
	}
	id := uuid.NewString()
	if err := q.WriteProfileInvestigation(ctx, tx, sqlc.WriteProfileInvestigationParams{ID: id, AppID: check.AppID, AccountID: accountID, Revision: 1, Investigation: body}); err != nil {
		return "", fmt.Errorf("save automatic investigation: %w", err)
	}
	if err := q.WriteProfileRegressionAssessment(ctx, tx, sqlc.WriteProfileRegressionAssessmentParams{ID: id, AppID: check.AppID, AccountID: accountID, Revision: 1, Assessment: assessment}); err != nil {
		return "", fmt.Errorf("save automatic assessment: %w", err)
	}
	return id, nil
}
