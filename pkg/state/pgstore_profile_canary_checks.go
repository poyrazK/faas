package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type profileCanaryCandidate struct {
	DeploymentID        string                      `json:"deployment_id"`
	AppID               string                      `json:"app_id"`
	AccountID           string                      `json:"account_id"`
	Scope               string                      `json:"scope"`
	CanaryStep          int                         `json:"canary_step"`
	CanaryStepStartedAt time.Time                   `json:"canary_step_started_at"`
	Policy              api.ProfileDeploymentPolicy `json:"policy"`
	StableCount         int                         `json:"stable_count"`
	StableID            *string                     `json:"stable_id"`
}

func decodeProfileCanarySignal(body []byte) (api.CanaryProfileSignal, error) {
	var out api.CanaryProfileSignal
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("decode canary profiling check: %w", err)
	}
	out.Options = api.NormalizeProfileRegressionOptions(out.Options)
	if out.Evidence == nil {
		out.Evidence = []api.ProfileRegressionEvidence{}
	}
	return out, nil
}

func (s *PgStore) GetProfileCanaryCheck(ctx context.Context, accountID, appID string, key ProfileCanaryCheckKey) (api.CanaryProfileSignal, error) {
	if key.CanaryStep < 0 || key.CanaryStep > math.MaxInt32 {
		return api.CanaryProfileSignal{}, ErrInvalidArgument
	}
	body, err := sqlc.New().ReadProfileCanaryCheck(ctx, s.pool, sqlc.ReadProfileCanaryCheckParams{
		DeploymentID: key.DeploymentID, AppID: appID, AccountID: accountID, CanaryStep: int32(key.CanaryStep),
		CanaryStepStartedAt: profileCheckTime(key.CanaryStepStartedAt), PolicyRevision: key.PolicyRevision,
	})
	if err != nil {
		return api.CanaryProfileSignal{}, routePolicyReadError(err)
	}
	return decodeProfileCanarySignal(body)
}

func (s *PgStore) GetLatestProfileCanaryCheck(ctx context.Context, accountID, appID, deploymentID string, policyRevision int64) (api.CanaryProfileSignal, error) {
	body, err := sqlc.New().ReadLatestProfileCanaryCheck(ctx, s.pool, sqlc.ReadLatestProfileCanaryCheckParams{
		DeploymentID: deploymentID, AppID: appID, AccountID: accountID, PolicyRevision: policyRevision,
	})
	if err != nil {
		return api.CanaryProfileSignal{}, routePolicyReadError(err)
	}
	return decodeProfileCanarySignal(body)
}

func (s *PgStore) ListProfileCanaryChecks(ctx context.Context, accountID, appID, deploymentID string, limit int, before string) (api.ProfileCanaryHistoryPage, error) {
	page := api.ProfileCanaryHistoryPage{AppID: appID, DeploymentID: deploymentID, Entries: []api.CanaryProfileSignal{}}
	cursor, err := validateProfileCanaryHistoryPage(limit, before, deploymentID)
	if err != nil {
		return page, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, fmt.Errorf("begin canary profile history page: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.ReadProfileCanaryHistoryTarget(ctx, tx, sqlc.ReadProfileCanaryHistoryTargetParams{AppID: appID, AccountID: accountID, DeploymentID: deploymentID}); err != nil {
		return page, routePolicyReadError(err)
	}
	var beforeStartedAt pgtype.Timestamptz
	var beforeStep pgtype.Int4
	var beforePolicyRevision pgtype.Int8
	if cursor != nil {
		if _, err := q.ReadProfileCanaryHistoryCursor(ctx, tx, sqlc.ReadProfileCanaryHistoryCursorParams{
			DeploymentID: deploymentID, AppID: appID, AccountID: accountID, CanaryStep: int32(cursor.CanaryStep),
			CanaryStepStartedAt: profileCheckTime(cursor.CanaryStepStartedAt), PolicyRevision: cursor.PolicyRevision,
		}); err != nil {
			return page, routePolicyReadError(err)
		}
		beforeStartedAt = profileCheckTime(cursor.CanaryStepStartedAt)
		beforeStep = pgtype.Int4{Int32: int32(cursor.CanaryStep), Valid: true}
		beforePolicyRevision = pgtype.Int8{Int64: cursor.PolicyRevision, Valid: true}
	}
	bodies, err := q.ListProfileCanaryChecks(ctx, tx, sqlc.ListProfileCanaryChecksParams{
		DeploymentID: deploymentID, AppID: appID, AccountID: accountID,
		BeforeStartedAt: beforeStartedAt, BeforeStep: beforeStep, BeforePolicyRevision: beforePolicyRevision,
		PageLimit: int32(limit + 1),
	})
	if err != nil {
		return page, fmt.Errorf("read canary profile history page: %w", err)
	}
	for i, body := range bodies {
		if i == limit {
			page.NextCursor = encodeProfileCanaryHistoryCursor(deploymentID, page.Entries[i-1])
			break
		}
		signal, err := decodeProfileCanarySignal(body)
		if err != nil {
			return page, err
		}
		if err := validateProfileCanaryHistorySignal(signal, deploymentID); err != nil {
			return page, err
		}
		page.Entries = append(page.Entries, signal)
	}
	if err := tx.Commit(ctx); err != nil {
		return page, err
	}
	return page, nil
}

func (s *PgStore) DiscoverProfileCanaryChecks(ctx context.Context, now time.Time) (int, error) {
	rows, err := sqlc.New().DiscoverProfileCanaryCandidates(ctx, s.pool, int32(api.ProfileAutoBatchSize))
	if err != nil {
		return 0, fmt.Errorf("discover canary profiling checks: %w", err)
	}
	total := 0
	for _, body := range rows {
		var candidate profileCanaryCandidate
		if err := json.Unmarshal(body, &candidate); err != nil {
			return total, fmt.Errorf("decode canary profiling candidate: %w", err)
		}
		count, err := s.enqueueProfileCanaryCheck(ctx, candidate, now)
		if err != nil {
			return total, err
		}
		total += count
	}
	return total, nil
}

func (s *PgStore) enqueueProfileCanaryCheck(ctx context.Context, c profileCanaryCandidate, now time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin canary profiling check: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: c.AppID, AccountID: c.AccountID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("lock canary profiling app: %w", err)
	}
	d := Deployment{ID: c.DeploymentID, AppID: c.AppID, Scope: c.Scope, CanaryStep: c.CanaryStep, CanaryStepStartedAt: &c.CanaryStepStartedAt}
	signal := newProfileCanarySignal(d, c.Policy, now)
	var next, completed pgtype.Timestamptz
	start := profileCanaryCaptureStart(d, c.Policy)
	end := start.Add(time.Duration(c.Policy.Config.WindowSeconds) * time.Second)
	candidate := api.ProfileQuery{DeploymentID: c.DeploymentID, Runtime: c.Policy.Config.Runtime, Start: start, End: end}
	signal.Candidate = &candidate
	if c.StableCount == 1 && c.StableID != nil && *c.StableID != "" {
		baseline := api.ProfileQuery{DeploymentID: *c.StableID, Runtime: c.Policy.Config.Runtime, Start: start, End: end}
		signal.Baseline = &baseline
		next = profileCheckTime(end.Add(api.ProfileAutoIngestionGrace))
		signal.NextAttemptAt = &next.Time
	} else {
		signal.Status = "inconclusive"
		if c.StableCount == 0 {
			signal.Reason = "A live stable deployment in the canary environment is not available."
		} else {
			signal.Reason = "A unique live stable deployment is not available for the profile comparison."
		}
		completed = profileCheckTime(now)
		signal.CompletedAt = &completed.Time
	}
	body, err := json.Marshal(signal)
	if err != nil {
		return 0, fmt.Errorf("encode canary profiling check: %w", err)
	}
	count, err := q.EnqueueProfileCanaryCheck(ctx, tx, sqlc.EnqueueProfileCanaryCheckParams{
		DeploymentID: c.DeploymentID, AppID: c.AppID, AccountID: c.AccountID, CanaryStep: int32(c.CanaryStep),
		CanaryStepStartedAt: profileCheckTime(c.CanaryStepStartedAt), PolicyRevision: c.Policy.Revision,
		Data: body, Status: signal.Status, Reason: signal.Reason, NextAttemptAt: next, CreatedAt: profileCheckTime(now), CompletedAt: completed,
	})
	if err != nil {
		return 0, fmt.Errorf("enqueue canary profiling check: %w", err)
	}
	return int(count), tx.Commit(ctx)
}

func (s *PgStore) MaintainProfileCanaryChecks(ctx context.Context, now time.Time) error {
	q := sqlc.New()
	if err := q.ExpireProfileCanaryChecks(ctx, s.pool, sqlc.ExpireProfileCanaryChecksParams{ObservedAt: profileCheckTime(now), MaxAttempts: api.ProfileAutoMaxAttempts}); err != nil {
		return fmt.Errorf("expire canary profiling checks: %w", err)
	}
	if err := q.PruneProfileCanaryChecks(ctx, s.pool, sqlc.PruneProfileCanaryChecksParams{Cutoff: profileCheckTime(now.Add(-api.ProfileAutoReceiptRetention)), BatchLimit: api.ProfileAutoBatchSize}); err != nil {
		return fmt.Errorf("prune canary profiling checks: %w", err)
	}
	return nil
}

func (s *PgStore) ClaimProfileCanaryCheck(ctx context.Context, now time.Time) (ProfileCanaryCheckWork, error) {
	token := uuid.NewString()
	row, err := sqlc.New().ClaimProfileCanaryCheck(ctx, s.pool, sqlc.ClaimProfileCanaryCheckParams{
		Token: token, ObservedAt: profileCheckTime(now), LeaseUntil: profileCheckTime(now.Add(api.ProfileAutoLeaseDuration)), MaxAttempts: api.ProfileAutoMaxAttempts,
	})
	if err != nil {
		return ProfileCanaryCheckWork{}, routePolicyReadError(err)
	}
	check, err := decodeProfileCanarySignal(row.Signal)
	return ProfileCanaryCheckWork{Check: profileGateWork(check), AppID: row.AppID, AccountID: row.AccountID, Token: token}, err
}

func (s *PgStore) FinishProfileCanaryCheck(ctx context.Context, work ProfileCanaryCheckWork, assessment api.ProfileRegressionAssessment, retry bool, now time.Time) (api.CanaryProfileSignal, error) {
	if err := validateProfileCanaryAssessment(assessment); err != nil {
		return api.CanaryProfileSignal{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.CanaryProfileSignal{}, fmt.Errorf("begin canary profiling result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: work.AppID, AccountID: work.AccountID}); err != nil {
		return api.CanaryProfileSignal{}, routePolicyReadError(err)
	}
	key := profileCanarySignalKey(work.Check)
	body, err := q.LockClaimedProfileCanaryCheck(ctx, tx, sqlc.LockClaimedProfileCanaryCheckParams{
		DeploymentID: key.DeploymentID, CanaryStep: int32(key.CanaryStep), CanaryStepStartedAt: profileCheckTime(key.CanaryStepStartedAt),
		PolicyRevision: key.PolicyRevision, AppID: work.AppID, AccountID: work.AccountID, Token: work.Token, ObservedAt: profileCheckTime(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.CanaryProfileSignal{}, ErrProfileCheckLease
	}
	if err != nil {
		return api.CanaryProfileSignal{}, fmt.Errorf("lock claimed canary profiling result: %w", err)
	}
	signal, err := decodeProfileCanarySignal(body)
	if err != nil {
		return signal, err
	}
	if err := validateProfileGateAssessment(signal, assessment); err != nil {
		return signal, err
	}
	signal.CheckedAt = &assessment.CheckedAt
	signal.Reason = assessment.Reason
	signal.Options, signal.Metric = assessment.Options, assessment.Options.Metric
	signal.BaselineRequests, signal.CandidateRequests = assessment.BaselineRequests, assessment.CandidateRequests
	signal.Attribution = assessment.Attribution
	signal.RequestMix = assessment.RequestMix
	signal.RouteChecks = assessment.RouteChecks
	signal.BaselineCoverage, signal.CandidateCoverage = assessment.BaselineCoverage, assessment.CandidateCoverage
	signal.Total, signal.Evidence, signal.UncomparableEntries = assessment.Total, assessment.Evidence, assessment.UncomparableEntries
	status := assessment.Status
	var next, completed pgtype.Timestamptz
	if retry && signal.Attempts < api.ProfileAutoMaxAttempts && (signal.Gate == nil || now.Before(signal.Gate.Deadline)) {
		status = "queued"
		next = profileCheckTime(now.Add(api.ProfileAutoRetryInterval))
		signal.Status = status
		signal.NextAttemptAt, signal.CompletedAt = &next.Time, nil
	} else {
		completed = profileCheckTime(now)
		signal.Status = status
		signal.NextAttemptAt, signal.CompletedAt = nil, &completed.Time
	}
	finishProfileGateWindow(&signal, assessment, retry, now)
	status = signal.Status
	next, completed = pgtype.Timestamptz{}, pgtype.Timestamptz{}
	if signal.NextAttemptAt != nil {
		next = profileCheckTime(*signal.NextAttemptAt)
	}
	if signal.CompletedAt != nil {
		completed = profileCheckTime(*signal.CompletedAt)
	}
	data, err := json.Marshal(signal)
	if err != nil {
		return signal, fmt.Errorf("encode canary profiling result: %w", err)
	}
	if err := q.FinishProfileCanaryCheck(ctx, tx, sqlc.FinishProfileCanaryCheckParams{
		DeploymentID: key.DeploymentID, CanaryStep: int32(key.CanaryStep), CanaryStepStartedAt: profileCheckTime(key.CanaryStepStartedAt),
		PolicyRevision: key.PolicyRevision, AppID: work.AppID, AccountID: work.AccountID, Token: work.Token,
		Data: data, Status: status, Attempts: int32(signal.Attempts), Reason: signal.Reason, NextAttemptAt: next, CompletedAt: completed,
	}); err != nil {
		return signal, fmt.Errorf("finish canary profiling result: %w", err)
	}
	stored, err := q.ReadProfileCanaryCheck(ctx, tx, sqlc.ReadProfileCanaryCheckParams{
		DeploymentID: key.DeploymentID, AppID: work.AppID, AccountID: work.AccountID, CanaryStep: int32(key.CanaryStep),
		CanaryStepStartedAt: profileCheckTime(key.CanaryStepStartedAt), PolicyRevision: key.PolicyRevision,
	})
	if err != nil {
		return signal, fmt.Errorf("read completed canary profiling result: %w", err)
	}
	if completed.Valid {
		scope, err := q.ReadProfileAlertDeploymentScope(ctx, tx, sqlc.ReadProfileAlertDeploymentScopeParams{AppID: work.AppID, AccountID: work.AccountID, DeploymentID: key.DeploymentID})
		if err != nil {
			return signal, err
		}
		slug, err := q.ReadProfileAlertOwner(ctx, tx, sqlc.ReadProfileAlertOwnerParams{AppID: work.AppID, AccountID: work.AccountID})
		if err != nil {
			return signal, err
		}
		o := profileAlertObservation{AppID: work.AppID, AccountID: work.AccountID, Scope: scope, Source: "canary", Revision: signal.PolicyRevision, Assessment: assessment, EvidencePath: "/v1/apps/" + slug + "/profiles/canary-checks/" + key.DeploymentID}
		plans, err := prepareProfileAlertsTx(ctx, tx, o)
		if err != nil {
			return signal, err
		}
		if profileAlertHasEvent(plans) {
			check := api.ProfileDeploymentCheck{AppID: work.AppID, DeploymentID: key.DeploymentID, Scope: scope, Baseline: &assessment.Baseline, Candidate: assessment.Candidate}
			id, err := saveAutoProfileInvestigationTx(ctx, tx, work.AccountID, check, assessment)
			if err != nil && !errors.Is(err, ErrProfileInvestigationQuota) {
				return signal, err
			}
			profileAlertInvestigation(plans, slug, id)
		}
		if err := publishProfileAlertsTx(ctx, tx, o, plans); err != nil {
			return signal, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return signal, err
	}
	return decodeProfileCanarySignal(stored)
}
