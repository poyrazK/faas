package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CanaryVerificationReleaser frees the rollout grant an older in-flight canary
// holds when a newer candidate cannot otherwise be verified (ADR-20261030102813
// amendment, Hunt 31 H31-4). An app runs at most maxReleases releases at once
// (api.TrafficSplitMaxReleases: its effective max_concurrency plus the rollout
// grant). With the default max_concurrency of 1 the serving deployment and an
// in-flight canary already use both, so the candidate's smoke is refused until
// its recovery window fails the deploy. The newer candidate would supersede the
// canary at go-live anyway; this moves that step to before its verification,
// only when the candidate does not fit.
type CanaryVerificationReleaser interface {
	// ReleaseCanariesForVerification supersedes the in-flight canaries of
	// candidateID's app and scope that are older than the candidate, when the
	// serving deployments plus the candidate exceed maxReleases and a serving
	// non-canary deployment remains to take their traffic. It returns the
	// released deployment IDs; none when the candidate fits.
	ReleaseCanariesForVerification(ctx context.Context, candidateID string, maxReleases int) ([]string, error)
}

var (
	_ CanaryVerificationReleaser = (*PgStore)(nil)
	_ CanaryVerificationReleaser = (*MemStore)(nil)
)

// verificationSibling is a live deployment beside a candidate under
// verification.
type verificationSibling struct {
	ID       string
	Prior    int
	Revision int
	Created  time.Time
	InFlight bool
}

// planVerificationRelease splits the candidate's live siblings into the
// canaries to release and the deployments that keep serving. It releases
// nothing when the candidate fits beside every serving sibling, or when no
// serving non-canary deployment would remain.
func planVerificationRelease(siblings []verificationSibling, revision int, created time.Time, maxReleases int) (release, keep []verificationSibling) {
	serving := 0
	for _, s := range siblings {
		if s.Prior > 0 {
			serving++
		}
	}
	if maxReleases <= 0 || serving+1 <= maxReleases {
		return nil, nil
	}
	for _, s := range siblings {
		older := s.Created.Before(created)
		if s.Revision > 0 && revision > 0 {
			older = s.Revision < revision
		}
		if s.InFlight && s.Prior > 0 && older {
			release = append(release, s)
		} else {
			keep = append(keep, s)
		}
	}
	weights := make([]struct {
		ID    string
		Prior int
	}, len(keep))
	for i, s := range keep {
		weights[i].ID, weights[i].Prior = s.ID, s.Prior
	}
	if len(release) == 0 || !supersedesInFlightCanaries(weights) {
		return nil, nil
	}
	return release, keep
}

func (s *PgStore) ReleaseCanariesForVerification(ctx context.Context, candidateID string, maxReleases int) ([]string, error) {
	if maxReleases <= 0 {
		return nil, nil
	}
	candidate, err := parsePgUUID(candidateID)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		released, err := s.releaseCanariesForVerificationOnce(ctx, candidate, candidateID, maxReleases)
		// The release locks deployment rows, then the account row through the
		// lifecycle successor trigger, like H31-2's transition.
		if !transientLockConflict(err) || attempt == imagePreparationTransitionAttempts {
			return released, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(time.Duration(attempt) * 50 * time.Millisecond):
		}
	}
}

func (s *PgStore) releaseCanariesForVerificationOnce(ctx context.Context, candidate pgtype.UUID, candidateID string, maxReleases int) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: release canaries for verification begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var appID, scope string
	var revision int
	var created time.Time
	if err := tx.QueryRow(ctx,
		`select app_id::text, scope, coalesce(revision, 0), created_at from deployments where id = $1`,
		candidate).Scan(&appID, &scope, &revision, &created); err != nil {
		return nil, fmt.Errorf("state: release canaries for verification load candidate: %w", mapErr(err))
	}
	rows, err := tx.Query(ctx,
		`select id::text, traffic_percent, coalesce(revision, 0), created_at,
		        canary_total_steps > 0 and coalesce(nullif(rollout_state, ''), 'pending') in ('pending','rolling_out')
		   from deployments
		  where app_id = $1 and scope = $2 and status = 'live' and id <> $3
		  order by id
		  for update`, appID, normalizedDeploymentScope(scope), candidate)
	if err != nil {
		return nil, fmt.Errorf("state: release canaries for verification lock siblings: %w", err)
	}
	var siblings []verificationSibling
	for rows.Next() {
		var sib verificationSibling
		if err := rows.Scan(&sib.ID, &sib.Prior, &sib.Revision, &sib.Created, &sib.InFlight); err != nil {
			rows.Close()
			return nil, fmt.Errorf("state: release canaries for verification scan sibling: %w", err)
		}
		siblings = append(siblings, sib)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: release canaries for verification iterate siblings: %w", err)
	}
	release, keep := planVerificationRelease(siblings, revision, created, maxReleases)
	if len(release) == 0 {
		return nil, nil
	}

	var now time.Time
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&now); err != nil {
		return nil, fmt.Errorf("state: release canaries for verification read clock: %w", err)
	}
	ids := make([]pgtype.UUID, len(release))
	released := make([]string, len(release))
	retired := make([]struct {
		ID    string
		Prior int
	}, len(release))
	for i, r := range release {
		if ids[i], err = parsePgUUID(r.ID); err != nil {
			return nil, err
		}
		released[i] = r.ID
		retired[i].ID, retired[i].Prior = r.ID, r.Prior
	}
	if _, err := sqlc.New().ReleaseCanariesForVerification(ctx, tx, sqlc.ReleaseCanariesForVerificationParams{
		AbortedAt: pgtype.Timestamptz{Time: now, Valid: true}, CandidateID: candidate, ReleasedIds: ids,
	}); err != nil {
		return nil, fmt.Errorf("state: release canaries for verification: %w", err)
	}
	weights := make([]struct {
		ID    string
		Prior int
	}, len(keep))
	for i, k := range keep {
		weights[i].ID, weights[i].Prior = k.ID, k.Prior
	}
	for i, w := range RedistributeTraffic(weights, 100) {
		if _, err := tx.Exec(ctx, `update deployments set traffic_percent = $2 where id = $1`, keep[i].ID, w); err != nil {
			return nil, fmt.Errorf("state: release canaries for verification restore %s: %w", keep[i].ID, err)
		}
	}
	if err := notifyRetiredCanarySiblingsTx(ctx, tx, appID, retired); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: release canaries for verification commit: %w", err)
	}
	return released, nil
}

func (m *MemStore) ReleaseCanariesForVerification(ctx context.Context, candidateID string, maxReleases int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cand, ok := m.deployments[candidateID]
	if !ok {
		return nil, ErrNotFound
	}
	var siblings []verificationSibling
	for id, d := range m.deployments {
		if id == candidateID || d.AppID != cand.AppID || normalizedDeploymentScope(d.Scope) != normalizedDeploymentScope(cand.Scope) || d.Status != DeployLive {
			continue
		}
		siblings = append(siblings, verificationSibling{ID: id, Prior: d.TrafficPercent, Revision: d.Revision, Created: d.CreatedAt, InFlight: rolloutInFlight(d)})
	}
	sort.Slice(siblings, func(i, j int) bool { return siblings[i].ID < siblings[j].ID })
	release, keep := planVerificationRelease(siblings, cand.Revision, cand.CreatedAt, maxReleases)
	if len(release) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	released := make([]string, 0, len(release))
	for _, r := range release {
		before := m.deployments[r.ID]
		other := before
		if m.deploymentRevisionRetainedLocked(r.ID) || m.deploymentInUsableReleaseLocked(r.ID) {
			other.Status = DeployLive
		} else {
			other.Status = DeploySuperseded
		}
		other.TrafficPercent = 0
		other.TrafficPercentExplicit = false
		m.endSupersededCanaryRolloutLocked(before, &other, candidateID, now)
		m.putDeploymentLocked(r.ID, other)
		released = append(released, r.ID)
	}
	weights := make([]siblingRow, len(keep))
	for i, k := range keep {
		weights[i] = siblingRow{ID: k.ID, Prior: k.Prior}
	}
	for i, w := range RedistributeTraffic(toHelperSiblings(weights), 100) {
		other := m.deployments[keep[i].ID]
		other.TrafficPercent = w
		m.putDeploymentLocked(keep[i].ID, other)
	}
	return released, nil
}
