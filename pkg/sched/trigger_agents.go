package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Agent trigger targets (ADR-20261027090010 §9). A broker trigger that names an agent
// admits one agent run per record instead of invoking its app, through the
// same admission as the agent API. The record succeeds once its run is
// admitted; agentd owns the run from there. Admission is idempotent per
// broker message, so a redelivered record admits nothing twice. Claims,
// acks, retries and dead letters are the trigger's own machinery.

// Codes of agent admission outcomes; classifyDLQReason maps them onto the
// dead-letter reasons.
const (
	triggerCodeAgentRefused     = "agent_target_refused"
	triggerCodeAgentLimit       = "agent_limit"
	triggerCodeAgentUnavailable = "agent_admission_error"
	triggerCodePayloadTooLarge  = "payload_too_large"
	triggerCodePayloadInvalid   = "broker_payload_invalid"
)

// TriggerAgents admits the runs of agent-target triggers;
// *agentstore.Store implements it.
type TriggerAgents interface {
	AdmitRun(ctx context.Context, a agentstore.Admission) (agentstore.Run, bool, error)
}

// WithTriggerAgents lets agent-target triggers run. Without it they are not
// polled, so their records stay with the broker.
func (l *Loop) WithTriggerAgents(agents TriggerAgents) *Loop {
	l.triggerAgents = agents
	return l
}

// triggerAgentInputs is the run's inputs: the trigger and the record.
type triggerAgentInputs struct {
	Trigger struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Slug string `json:"slug"`
	} `json:"trigger"`
	Record triggerAgentRecord `json:"record"`
}

// triggerAgentRecord is one record. Its payload is JSON: trigger_records
// stores every polled payload as jsonb before it is claimed.
type triggerAgentRecord struct {
	ItemIdentifier string            `json:"item_identifier"`
	Payload        json.RawMessage   `json:"payload"`
	Headers        map[string]string `json:"headers,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
}

func (l *Loop) admitAgentTriggerBatch(ctx context.Context, store storeLike, t sqlc.Trigger, batch []SourceRecord, claimed []sqlc.TriggerRecord) triggerDispatchResponse {
	response := triggerDispatchResponse{Results: make([]triggerDispatchResult, 0, len(batch))}
	limits, err := triggerAgentLimits(ctx, store, t.AccountID.String())
	claimedByItem := make(map[string]sqlc.TriggerRecord, len(claimed))
	for _, record := range claimed {
		claimedByItem[record.ItemIdentifier] = record
	}
	for _, record := range batch {
		result := triggerDispatchResult{ItemIdentifier: record.ItemIdentifier, Status: "retry", Code: triggerCodeAgentUnavailable,
			Error: "agent run admission temporarily failed"}
		if err == nil {
			result = l.admitAgentTriggerRecord(ctx, t, record, claimedByItem[record.ItemIdentifier], limits)
		}
		response.Results = append(response.Results, result)
	}
	return response
}

func triggerAgentLimits(ctx context.Context, store storeLike, accountID string) (agentstore.AdmitLimits, error) {
	accounts, ok := store.(interface {
		AccountByID(ctx context.Context, id string) (state.Account, error)
	})
	if !ok {
		return agentstore.AdmitLimits{}, errors.New("trigger agents need the account store")
	}
	account, err := accounts.AccountByID(ctx, accountID)
	if err != nil {
		return agentstore.AdmitLimits{}, fmt.Errorf("read trigger account: %w", err)
	}
	return agentstore.PlanLimits(account.Plan), nil
}

func (l *Loop) admitAgentTriggerRecord(ctx context.Context, t sqlc.Trigger, record SourceRecord, claimed sqlc.TriggerRecord, limits agentstore.AdmitLimits) triggerDispatchResult {
	result := triggerDispatchResult{ItemIdentifier: record.ItemIdentifier}
	inputs, code, err := triggerAgentRunInputs(t, record)
	if err != nil {
		result.Status, result.Code, result.Error = "dead_letter", code, err.Error()
		return result
	}
	// The broker's message identity names a redelivery; without one, the
	// record's row does, since a redelivered item reuses it.
	identity := record.StableIdentifier
	if identity == "" && claimed.ID.Valid {
		identity = claimed.ID.String()
	}
	if identity == "" {
		result.Status, result.Code, result.Error = "retry", triggerCodeAgentUnavailable, "the record has no durable identity yet"
		return result
	}
	digest := sha256.Sum256([]byte(t.ID.String() + "\x00" + identity))
	_, _, err = l.triggerAgents.AdmitRun(ctx, agentstore.Admission{AccountID: t.AccountID.String(), Agent: t.Agent.String,
		Trigger: "trigger", DedupeKey: "trigger:" + hex.EncodeToString(digest[:]), Inputs: inputs, Limits: limits})
	result.Status, result.Code, result.Error = agentTriggerOutcome(err)
	if result.Status == "retry" && result.Code == triggerCodeAgentUnavailable {
		l.log.Warn("sched trigger tick: agent run admission", "trigger_id", t.ID.String(), "record_id", record.ItemIdentifier, "err", err)
	}
	return result
}

// triggerAgentRunInputs builds the run's inputs, bounded like any run's. A
// failure names the dead-letter code.
func triggerAgentRunInputs(t sqlc.Trigger, record SourceRecord) (json.RawMessage, string, error) {
	var in triggerAgentInputs
	in.Trigger.ID, in.Trigger.Kind, in.Trigger.Slug = t.ID.String(), t.Kind, t.Slug
	in.Record = triggerAgentRecord{ItemIdentifier: record.ItemIdentifier, Payload: record.Payload, Headers: record.Headers, Metadata: record.Metadata}
	if len(in.Record.Payload) == 0 {
		in.Record.Payload = json.RawMessage("{}")
	}
	if !json.Valid(in.Record.Payload) {
		return nil, triggerCodePayloadInvalid, errors.New("the record's payload is not JSON")
	}
	inputs, err := json.Marshal(in)
	if err != nil {
		return nil, triggerCodePayloadInvalid, fmt.Errorf("encode agent run inputs: %w", err)
	}
	if len(inputs) > api.AgentRunInputsMaxBytes {
		return nil, triggerCodePayloadTooLarge, fmt.Errorf("the record makes agent run inputs of %d bytes; the limit is %d", len(inputs), api.AgentRunInputsMaxBytes)
	}
	return inputs, "", nil
}

// agentTriggerOutcome maps an admission error onto a record outcome. A plan
// limit passes, so the record retries; a refusal no retry can fix
// dead-letters it.
func agentTriggerOutcome(err error) (status, code, message string) {
	var limit *agentstore.LimitError
	switch {
	case err == nil:
		return "succeeded", "", ""
	case errors.As(err, &limit):
		return "retry", triggerCodeAgentLimit, fmt.Sprintf("agent run refused by the plan limit %s (%d)", limit.Limit, limit.Max)
	case errors.Is(err, agentstore.ErrNotFound):
		return "dead_letter", triggerCodeAgentRefused, "the trigger's agent no longer exists"
	case errors.Is(err, agentstore.ErrDeliveryTargetMissing):
		return "dead_letter", triggerCodeAgentRefused, "the agent declares a GitHub comment, which a trigger's run has no target for"
	case errors.Is(err, agentstore.ErrTenantNotFound), errors.Is(err, agentstore.ErrTenantInactive),
		errors.Is(err, agentstore.ErrGitHubUnavailable), errors.Is(err, agentstore.ErrCheckoutUnavailable):
		return "dead_letter", triggerCodeAgentRefused, "the agent run cannot be admitted for this account"
	}
	return "retry", triggerCodeAgentUnavailable, "agent run admission temporarily failed"
}
