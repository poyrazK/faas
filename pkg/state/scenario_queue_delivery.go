// adr: 20261023111825
package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// scenarioQueueDeliveryTarget is what decides a same-run delivery: the
// receiving member's app, plan and retry policy, whether a queue binding or
// trigger would choose its consumer, its live deployments, and how many sends
// the run already delivered.
type scenarioQueueDeliveryTarget struct {
	appID       string
	plan        api.Plan
	retryPolicy json.RawMessage
	bound       bool
	live        int64
	delivered   int64
}

// scenarioDeliverableQueueSend reports a captured send the generic drain
// would dispatch to the default handler: the unnamed queue, with no work
// policy, environment or flag context. Anything else needs production queue
// selection, which lives in apid.
func scenarioDeliverableQueueSend(msg ScenarioQueueCapture) (api.QueueSendRequest, bool) {
	var req api.QueueSendRequest
	if msg.Oversized || msg.QueueName != "" {
		return req, false
	}
	dec := json.NewDecoder(bytes.NewReader(msg.Message))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil {
		return req, false
	}
	return req, req.QueueName == "" && req.Environment == "" && req.FlagContext == "" && req.Work == nil
}

// scenarioQueueDelivery decides a same-run delivery candidate's result and,
// when delivered, the queue invocation of the receiving member.
func scenarioQueueDelivery(msg ScenarioQueueCapture, target scenarioQueueDeliveryTarget, accountID string, now time.Time) (string, Invocation) {
	req, deliverable := scenarioDeliverableQueueSend(msg)
	limits, known := api.LimitsFor(target.plan)
	switch {
	case !deliverable || target.bound || target.live != 1 || !known || limits.MaxQueueDepth == 0:
		return api.ScenarioQueueUnsupported, Invocation{}
	case target.delivered >= api.MaxScenarioDeliveredQueueMessages:
		return api.ScenarioQueueOverflow, Invocation{}
	}
	return api.ScenarioQueueDelivered, Invocation{AppID: target.appID, AccountID: accountID, Source: InvocationQueue, State: InvocationPending,
		Payload: req.Payload, Headers: json.RawMessage(`{}`), DueAt: now,
		RetryPolicyJSON: scenarioDeliveryRetryPolicy(target.retryPolicy, req.RetryPolicy, limits.MaxQueueAttempts)}
}

// scenarioDeliveryRetryPolicy mirrors apid's effective queue retry policy:
// the send's own, else the app's, capped at the plan's attempts.
func scenarioDeliveryRetryPolicy(appPolicy json.RawMessage, override *api.RetryPolicyDTO, planLimit int) json.RawMessage {
	policy := api.RetryPolicyDTO{}
	if override != nil {
		policy = *override
	} else if len(appPolicy) > 0 && string(appPolicy) != "{}" {
		_ = json.Unmarshal(appPolicy, &policy)
	}
	policy.MaxAttempts = api.EffectiveRetryMaxAttempts(policy.MaxAttempts, planLimit)
	raw, _ := json.Marshal(policy)
	return raw
}

func (s *PgStore) scenarioQueueDeliveryTarget(ctx context.Context, tx sqlc.DBTX, accountID, runID, workload string) (scenarioQueueDeliveryTarget, error) {
	row, err := sqlc.New().ScenarioQueueDeliveryTarget(ctx, tx, sqlc.ScenarioQueueDeliveryTargetParams{AccountID: accountID, RunID: runID, Workload: workload})
	if errors.Is(err, pgx.ErrNoRows) {
		return scenarioQueueDeliveryTarget{}, ErrNotFound
	}
	if err != nil {
		return scenarioQueueDeliveryTarget{}, fmt.Errorf("resolve rehearsal queue delivery: %w", err)
	}
	return scenarioQueueDeliveryTarget{appID: row.AppID, plan: api.Plan(row.Plan), retryPolicy: row.RetryPolicy, bound: row.Bound, live: row.Live, delivered: row.Delivered}, nil
}

// scenarioQueueDeliveryTargetLocked mirrors ScenarioQueueDeliveryTarget.
func (m *MemStore) scenarioQueueDeliveryTargetLocked(accountID, runID, workload string) (scenarioQueueDeliveryTarget, bool) {
	for _, p := range m.scenarioActionPolicies {
		if !sameScenarioAccount(p.Member.AccountID, accountID) || p.Member.RunID != runID || p.Member.Workload != workload {
			continue
		}
		app := m.apps[p.Member.AppID]
		target := scenarioQueueDeliveryTarget{appID: app.ID, plan: m.accounts[p.Member.AccountID].Plan, retryPolicy: app.RetryPolicyJSON}
		for _, binding := range m.queueBindings {
			target.bound = target.bound || sameMemUUID(binding.AppID, app.ID)
		}
		for _, trigger := range m.triggers {
			target.bound = target.bound || sameMemUUID(pgUUIDString(trigger.AppID), app.ID) && trigger.Kind == "queue"
		}
		for _, d := range m.deployments {
			if d.AppID == app.ID && d.Status == DeployLive {
				target.live++
			}
		}
		for _, row := range m.scenarioQueueCaptures {
			if sameScenarioAccount(row.accountID, accountID) && row.runID == runID && row.delivery == api.ScenarioQueueDelivered {
				target.delivered++
			}
		}
		return target, true
	}
	return scenarioQueueDeliveryTarget{}, false
}
