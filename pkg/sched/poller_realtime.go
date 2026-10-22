package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// realtimeGroupStore is the consumer-group surface the realtime poller
// drives, plus the ledger transition for deliveries the group takes back.
type realtimeGroupStore interface {
	ClaimRealtimeConsumerGroupBatch(ctx context.Context, ep, ch, name, worker string, limit int) (state.RealtimeConsumerGroupBatch, error)
	SettleRealtimeConsumerGroup(ctx context.Context, ep, ch, name string, req state.RealtimeConsumerGroupSettlement) (state.RealtimeConsumerGroupSettlementResult, error)
	ListRealtimeConsumerGroups(ctx context.Context, ep, ch string) ([]state.RealtimeConsumerGroup, error)
	state.TriggerRecordSuperseder
}

// realtimePoller delivers a bound consumer group's messages (ADR-1322). The
// group owns attempts, ordering, retries and dead letters; this poller maps
// dispatch outcomes onto group settlements. Each claim is one delivery with
// its own item identifier, so the trigger ledger never retries it.
type realtimePoller struct {
	store     realtimeGroupStore
	cfg       api.RealtimeTriggerConfig
	triggerID string

	mu     sync.Mutex
	claims map[string]state.RealtimeConsumerGroupClaim
}

// realtimeTriggerRecord is the JSON body one invocation receives.
type realtimeTriggerRecord struct {
	EndpointID   string            `json:"endpoint_id"`
	Channel      string            `json:"channel"`
	Group        string            `json:"group"`
	Sequence     string            `json:"sequence"`
	MessageID    string            `json:"message_id,omitempty"`
	Event        string            `json:"event"`
	Version      int64             `json:"version"`
	Deleted      bool              `json:"deleted"`
	DataBase64   string            `json:"data_base64"`
	Binary       bool              `json:"binary"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	Attempt      int               `json:"attempt"`
	FencingToken string            `json:"fencing_token"`
}

func newRealtimePoller(store state.Store, t sqlc.Trigger) (*realtimePoller, error) {
	groups, ok := store.(realtimeGroupStore)
	if !ok {
		return nil, errors.New("poller_realtime: state store has no realtime consumer groups")
	}
	var cfg api.RealtimeTriggerConfig
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return nil, fmt.Errorf("poller_realtime: decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("poller_realtime: %w", err)
	}
	return &realtimePoller{store: groups, cfg: cfg, triggerID: t.ID.String(),
		claims: map[string]state.RealtimeConsumerGroupClaim{}}, nil
}

func (p *realtimePoller) Kind() string { return string(api.TriggerKindRealtime) }

func (p *realtimePoller) dispatchContext(ctx context.Context) context.Context {
	return state.WithRealtimeTriggerDispatch(ctx, p.triggerID)
}

// Poll claims up to the trigger's batch size (one for realtime triggers).
// Paused, rate-limited, circuit-open or caught-up groups return no records.
func (p *realtimePoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	batch, err := p.store.ClaimRealtimeConsumerGroupBatch(p.dispatchContext(ctx), p.cfg.EndpointID, p.cfg.Channel, p.cfg.Group,
		"trigger:"+p.triggerID, max(int(t.BatchSizeMax), 1))
	if err != nil {
		return PollResult{Records: []SourceRecord{}, Error: fmt.Errorf("poller_realtime: claim: %w", err)}
	}
	records := make([]SourceRecord, 0, len(batch.Claims))
	now := time.Now().UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, claim := range batch.Claims {
		body, err := json.Marshal(realtimeTriggerRecord{
			EndpointID: p.cfg.EndpointID, Channel: p.cfg.Channel, Group: p.cfg.Group,
			Sequence: claim.Message.Sequence, MessageID: claim.Message.TargetMessageID, Event: claim.Message.Event,
			Version: claim.Message.Version, Deleted: claim.Message.Deleted, DataBase64: claim.Message.DataBase64,
			Binary: claim.Message.Binary, Metadata: claim.Message.Metadata, CreatedAt: claim.Message.CreatedAt,
			Attempt: claim.Attempt, FencingToken: claim.FencingToken,
		})
		if err != nil {
			return PollResult{Records: records, Error: fmt.Errorf("poller_realtime: encode record: %w", err)}
		}
		// The fencing token is unique per claim, so every delivery has its
		// own ledger row and a group retry is never mistaken for a replay.
		id := claim.Message.Sequence + ":" + claim.FencingToken
		p.claims[id] = claim
		records = append(records, SourceRecord{ItemIdentifier: id, Payload: body, ReceivedAt: now,
			Metadata: map[string]any{"sequence": claim.Message.Sequence, "attempt": claim.Attempt}})
	}
	return PollResult{Records: records}
}

func (p *realtimePoller) take(ids []string) map[string]state.RealtimeConsumerGroupClaim {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]state.RealtimeConsumerGroupClaim, len(ids))
	for _, id := range ids {
		if claim, ok := p.claims[id]; ok {
			out[id] = claim
			delete(p.claims, id)
		}
	}
	return out
}

// Ack completes successful deliveries. A lost lease means the group already
// expired the claim and will redeliver, which the ledger cannot prevent.
func (p *realtimePoller) Ack(ctx context.Context, _ sqlc.Trigger, ids []string) error {
	var errs []error
	for id, claim := range p.take(ids) {
		_, err := p.store.SettleRealtimeConsumerGroup(p.dispatchContext(ctx), p.cfg.EndpointID, p.cfg.Channel, p.cfg.Group,
			state.RealtimeConsumerGroupSettlement{Sequence: claim.Message.Sequence, ClaimToken: claim.ClaimToken, Action: "complete"})
		if err != nil && !errors.Is(err, state.ErrRealtimeConsumerGroupLease) {
			errs = append(errs, fmt.Errorf("complete %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// Nack returns deliveries the dispatcher could not attempt (claim, rate-limit
// or transport failures) without spending the handler's attempt.
func (p *realtimePoller) Nack(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	codes := make(map[string]string, len(ids))
	for _, id := range ids {
		codes[id] = "dispatch_" + reason
	}
	return p.NackWithCodes(ctx, t, codes)
}

// NackWithCodes settles each failed delivery on the group and ends its
// ledger records. Platform failures release the attempt; handler failures
// retry with the gateway's code, so the group's retry policy (including
// permanent failure codes) decides whether the message is dead.
func (p *realtimePoller) NackWithCodes(ctx context.Context, t sqlc.Trigger, codes map[string]string) error {
	ids := make([]string, 0, len(codes))
	for id := range codes {
		ids = append(ids, id)
	}
	var errs []error
	for id, claim := range p.take(ids) {
		code := codes[id]
		req := state.RealtimeConsumerGroupSettlement{Sequence: claim.Message.Sequence, ClaimToken: claim.ClaimToken, Action: "release"}
		if !realtimePlatformFailure(code) {
			req.Action, req.FailureCode = "retry", realtimeFailureCode(code)
		}
		_, err := p.store.SettleRealtimeConsumerGroup(p.dispatchContext(ctx), p.cfg.EndpointID, p.cfg.Channel, p.cfg.Group, req)
		if err != nil && !errors.Is(err, state.ErrRealtimeConsumerGroupLease) {
			errs = append(errs, fmt.Errorf("%s %s: %w", req.Action, id, err))
		}
	}
	if err := p.store.SupersedeTriggerRecordItems(ctx, t.ID.String(), ids, "returned to realtime consumer group"); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// realtimePlatformFailure reports whether the handler never ran: the
// dispatcher's own failure paths and the gateway's invoke and batch errors.
func realtimePlatformFailure(code string) bool {
	switch code {
	case "invoke_error", "batch_timeout":
		return true
	}
	return strings.HasPrefix(code, "dispatch_")
}

// realtimeFailureCode keeps a gateway code within the group's failure-code
// bounds (printable ASCII, RealtimeConsumerGroupFailureMaxBytes).
func realtimeFailureCode(code string) string {
	if code == "" {
		return "handler_failed"
	}
	out := make([]byte, 0, min(len(code), api.RealtimeConsumerGroupFailureMaxBytes))
	for i := 0; i < len(code) && len(out) < api.RealtimeConsumerGroupFailureMaxBytes; i++ {
		if c := code[i]; c >= 32 && c <= 126 {
			out = append(out, c)
		}
	}
	return string(out)
}

// BrokerStats reports the group's waiting work, unassigned plus ready, so a
// queue_lag scaling target scales the app out ahead of a realtime backlog.
func (p *realtimePoller) BrokerStats(ctx context.Context, _ sqlc.Trigger) BrokerStats {
	groups, err := p.store.ListRealtimeConsumerGroups(ctx, p.cfg.EndpointID, p.cfg.Channel)
	if err != nil {
		return BrokerStats{}
	}
	for _, g := range groups {
		if g.Name != p.cfg.Group {
			continue
		}
		unassigned, _ := strconv.ParseInt(g.Unassigned, 10, 64)
		waiting := unassigned + int64(g.Ready)
		return BrokerStats{Lag: waiting, Depth: waiting, Available: true}
	}
	return BrokerStats{}
}

func (p *realtimePoller) Close() error { return nil }
