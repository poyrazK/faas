package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// RolloutDecisionStore records and reads the evidence behind rollout
// decisions (ADR-1447). It is separate from Store so narrow test doubles do
// not grow; PgStore and MemStore implement it and APID type-asserts it.
type RolloutDecisionStore interface {
	RecoverRolloutWithDecision(ctx context.Context, appID string, action, reason string, decision json.RawMessage) (Deployment, int64, error)
	RecoverRolloutForDeploymentWithDecision(ctx context.Context, appID, deploymentID, expectedPredecessorID, action, reason string, decision json.RawMessage) (Deployment, int64, error)
	RecordRolloutHold(ctx context.Context, params RolloutHoldParams) (recorded bool, auditID int64, err error)
	LatestRolloutHold(ctx context.Context, deploymentID string) (json.RawMessage, error)
}

// RolloutHoldParams describes one hold the canary progression observed. The
// hold is recorded only while the deployment is a live, in-flight canary on
// ExpectedStep, and only when it is not a repeat of the step's newest hold
// row: a different rule, or the same rule at least
// api.RolloutHoldRefreshInterval later.
type RolloutHoldParams struct {
	DeploymentID string
	AccountID    *uuid.UUID
	ExpectedStep int
	// Rule is the decision's closed-set rule; Decision is the validated
	// api.RolloutDecision it came from.
	Rule     string
	Decision json.RawMessage
	Actor    string
	At       time.Time
}

func (p RolloutHoldParams) valid() bool {
	return p.DeploymentID != "" && p.ExpectedStep >= 0 && api.RolloutRuleKind(p.Rule) == api.RolloutDecisionHold &&
		len(p.Decision) > 0 && json.Valid(p.Decision) && p.Actor != "" && !p.At.IsZero()
}

// RecordRolloutHold inserts a deploy.rollout_held audit row unless it repeats
// the newest hold row of the current step. The deployment row is never
// written: several triggers fire on any update of deployments.
func (s *PgStore) RecordRolloutHold(ctx context.Context, p RolloutHoldParams) (bool, int64, error) {
	if !p.valid() {
		return false, 0, ErrRolloutStateInvalid
	}
	data := withAuditDecision([]byte(`{}`), p.Decision)
	var id int64
	err := s.pool.QueryRow(ctx,
		`with d as (
		    select id, canary_step_started_at
		      from deployments
		     where id = $1::uuid and status = 'live' and canary_total_steps > 0
		       and canary_step = $2
		       and coalesce(nullif(rollout_state, ''), 'pending') in ('pending', 'rolling_out')
		 ), latest as (
		    select a.at, a.data->'decision'->>'rule' as rule
		      from deployment_audit a, d
		     where a.deployment_id = d.id and a.kind = 'deploy.rollout_held'
		       and a.at >= d.canary_step_started_at
		     order by a.at desc, a.id desc
		     limit 1
		 )
		 insert into deployment_audit (deployment_id, account_id, kind, actor, at, data)
		 select d.id, $3, 'deploy.rollout_held', $4, $5::timestamptz, $6::jsonb
		   from d
		  where not exists (
		    select 1 from latest
		     where latest.rule = $7::text
		       and latest.at > $5::timestamptz - make_interval(secs => $8::double precision)
		  )
		 returning id`,
		p.DeploymentID, p.ExpectedStep, p.AccountID, p.Actor, p.At, data, p.Rule,
		api.RolloutHoldRefreshInterval.Seconds()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("state: record rollout hold: %w", err)
	}
	return true, id, nil
}

// LatestRolloutHold returns the newest hold recorded since the deployment's
// current canary step started, or nil when the rollout is not in flight or
// has no hold for this step.
func (s *PgStore) LatestRolloutHold(ctx context.Context, deploymentID string) (json.RawMessage, error) {
	var decision []byte
	err := s.pool.QueryRow(ctx,
		`select a.data->'decision'
		   from deployment_audit a
		   join deployments d on d.id = a.deployment_id
		  where a.deployment_id = $1::uuid and a.kind = 'deploy.rollout_held'
		    and d.status = 'live' and d.canary_total_steps > 0
		    and coalesce(nullif(d.rollout_state, ''), 'pending') in ('pending', 'rolling_out')
		    and a.at >= d.canary_step_started_at
		  order by a.at desc, a.id desc
		  limit 1`, deploymentID).Scan(&decision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: latest rollout hold: %w", err)
	}
	if len(decision) == 0 {
		return nil, nil
	}
	return json.RawMessage(decision), nil
}

// RecordRolloutHold mirrors PgStore.
func (m *MemStore) RecordRolloutHold(_ context.Context, p RolloutHoldParams) (bool, int64, error) {
	if !p.valid() {
		return false, 0, ErrRolloutStateInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deploymentByAnyIDLocked(p.DeploymentID)
	if !ok || !rolloutInFlight(d) || d.CanaryStep != p.ExpectedStep {
		return false, 0, nil
	}
	if latest, ok := m.latestRolloutHoldLocked(d); ok {
		var prior struct {
			Decision struct {
				Rule string `json:"rule"`
			} `json:"decision"`
		}
		if json.Unmarshal(latest.Data, &prior) == nil && prior.Decision.Rule == p.Rule &&
			latest.At.After(p.At.Add(-api.RolloutHoldRefreshInterval)) {
			return false, 0, nil
		}
	}
	id, err := m.appendDeploymentAuditLocked(DeploymentAudit{
		DeploymentID: uuid.MustParse(d.ID), AccountID: p.AccountID, Kind: DeployRolloutHeld,
		Actor: p.Actor, At: p.At, Data: json.RawMessage(withAuditDecision([]byte(`{}`), p.Decision)),
	})
	if err != nil {
		return false, 0, err
	}
	return true, id, nil
}

// LatestRolloutHold mirrors PgStore.
func (m *MemStore) LatestRolloutHold(_ context.Context, deploymentID string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deploymentByAnyIDLocked(deploymentID)
	if !ok || !rolloutInFlight(d) {
		return nil, nil
	}
	latest, ok := m.latestRolloutHoldLocked(d)
	if !ok {
		return nil, nil
	}
	var row struct {
		Decision json.RawMessage `json:"decision"`
	}
	if err := json.Unmarshal(latest.Data, &row); err != nil || len(row.Decision) == 0 {
		return nil, nil
	}
	return row.Decision, nil
}

func (m *MemStore) latestRolloutHoldLocked(d Deployment) (DeploymentAudit, bool) {
	want, err := uuid.Parse(d.ID)
	if err != nil {
		return DeploymentAudit{}, false
	}
	for i := len(m.deploymentAudit) - 1; i >= 0; i-- {
		a := m.deploymentAudit[i]
		if a.DeploymentID != want || a.Kind != DeployRolloutHeld {
			continue
		}
		if d.CanaryStepStartedAt != nil && a.At.Before(*d.CanaryStepStartedAt) {
			return DeploymentAudit{}, false
		}
		return a, true
	}
	return DeploymentAudit{}, false
}

func (m *MemStore) deploymentByAnyIDLocked(id string) (Deployment, bool) {
	if d, ok := m.deployments[id]; ok {
		return d, true
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return Deployment{}, false
	}
	for key, d := range m.deployments {
		if other, err := uuid.Parse(key); err == nil && other == parsed {
			return d, true
		}
	}
	return Deployment{}, false
}

func rolloutInFlight(d Deployment) bool {
	state := NormalizeRolloutState(d.RolloutState)
	return d.Status == DeployLive && d.CanaryTotalSteps > 0 && (state == "pending" || state == "rolling_out")
}

func cloneRawJSON(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), b...)
}

// rolloutDecisionOfKind decodes a stored decision only when it explains an
// outcome of the given kind (ADR-1447): an advance record for a completed
// rollout, an abort record for an aborted one.
func rolloutDecisionOfKind(raw json.RawMessage, kind string) *api.RolloutDecision {
	if len(raw) == 0 {
		return nil
	}
	var decision api.RolloutDecision
	if err := json.Unmarshal(raw, &decision); err != nil || decision.Kind != kind {
		return nil
	}
	return &decision
}
