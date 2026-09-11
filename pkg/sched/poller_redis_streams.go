// poller_redis_streams.go — Redis Streams consumer-group poller
// (issue #757 / ADR-0NN, commit #11 of feat-triggers-mega).
//
// One redis/go-redis/v9 client per kind=redis_streams trigger
// (Redis has no connection multiplexing across consumers; one
// trigger = one XReadGroup consumer = one client). Pulls entries
// via XReadGroup, hands them to the dispatch tick as
// SourceRecord slices.
//
// Ack: XAck(stream, group, ids...) — moves the entries from the
// consumer's pending-entries-list (PEL) to the "Acked" side of
// the stream so they won't be redelivered.
//
// Nack: XClaim after the visibility-timeout — the broker's PEL
// re-entry primitive. We don't try to mutate the message; we
// tell Redis to re-deliver it to the same consumer (or anyone in
// the group) after the idle window expires.
//
// poison_record becomes a no-op Ack (the dispatcher has already
// recorded the dead-letter row — redelivery would just create
// duplicates).

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// redisPoller wraps a redis.Client + the per-trigger consumer
// name. The trigger's config gives us stream+group; the consumer
// name is a stable identifier per trigger so multiple schedd
// instances can share the group.
type redisPoller struct {
	client     *redis.Client
	stream     string
	group      string
	consumer   string
	addr       string
	batchMax   int
	visibility time.Duration

	mu       sync.Mutex
	inFlight map[string]string // id → original stream value (for header passthrough)

	// closeOnce + closeErr make Close idempotent (review
	// finding #7). The underlying redis.Client uses atomic CAS
	// internally and returns redis.ErrClosed on the second call;
	// wrapping in sync.Once avoids surfacing that error twice
	// for callers (leakcheck's TestTriggerPollers_*) which
	// expect Close#2 == nil.
	closeOnce sync.Once
	closeErr  error
}

// redisConfig is the per-kind config blob.
//
// Schema (validated in pkg/gregalemanifest.validateKindConfig):
//
//	{
//	  "addr":   "redis:6379",
//	  "stream": "cacheinvalids",
//	  "group":  "faas-cache"
//	}
type redisConfig struct {
	Addr   string `json:"addr"`
	Stream string `json:"stream"`
	Group  string `json:"group"`
}

func decodeRedisConfig(t sqlc.Trigger) (redisConfig, error) {
	var cfg redisConfig
	if len(t.Config) == 0 {
		return cfg, fmt.Errorf("redis_poller: trigger missing config")
	}
	if err := json.Unmarshal(t.Config, &cfg); err != nil {
		return cfg, fmt.Errorf("redis_poller: decode config: %w", err)
	}
	if cfg.Addr == "" {
		return cfg, fmt.Errorf("redis_poller: trigger missing addr")
	}
	if cfg.Stream == "" {
		return cfg, fmt.Errorf("redis_poller: trigger missing stream")
	}
	if cfg.Group == "" {
		return cfg, fmt.Errorf("redis_poller: trigger missing group")
	}
	return cfg, nil
}

// newRedisPoller constructs the poller and ensures the consumer
// group exists (creates it with MKSTREAM if missing).
//
// Consumer name: "faas-<trigger-id>". Unique per trigger so
// multiple schedd replicas load-balance across the group.
//
// Visibility / claim idle: 30s. A message that hasn't been acked
// within 30s can be claimed by another consumer — this is the
// per-trigger "retry after 30s" budget.
func newRedisPoller(t sqlc.Trigger) (triggerSource, error) {
	cfg, err := decodeRedisConfig(t)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Dialer:       oci.EgressDialContext(&net.Dialer{Timeout: 5 * time.Second}),
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     4,
	})
	// Verify the connection up-front — a misconfigured addr is a
	// startup failure, not a per-tick surprise.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis_poller: ping %s: %w", cfg.Addr, err)
	}
	consumer := "faas-" + strings.ReplaceAll(t.ID.String(), "-", "")
	// Create the group if missing. BUSYGROUP error means the
	// group already exists — that's fine.
	if err := client.XGroupCreateMkStream(ctx, cfg.Stream, cfg.Group, "$").Err(); err != nil &&
		!strings.Contains(err.Error(), "BUSYGROUP") {
		_ = client.Close()
		return nil, fmt.Errorf("redis_poller: create group %s/%s: %w", cfg.Stream, cfg.Group, err)
	}
	return &redisPoller{
		client:     client,
		stream:     cfg.Stream,
		group:      cfg.Group,
		consumer:   consumer,
		addr:       cfg.Addr,
		batchMax:   100,
		visibility: 30 * time.Second,
		inFlight:   map[string]string{},
	}, nil
}

// Kind returns the trigger kind this poller handles.
func (r *redisPoller) Kind() string { return "redis_streams" }

// Poll reads up to batchMax entries via XReadGroup.
//
// Two passes (audit finding #4):
//
//  1. id="0" with Block=50ms — drains the consumer's PEL
//     (entries delivered earlier but never acked). Without
//     this pass, every PEL entry is leaked until the consumer
//     re-claims it via XClaim after the visibility timeout.
//  2. id=">" with Block=250ms — new entries that haven't
//     been delivered to any consumer yet.
//
// Both passes share the same Count (limit) so the dispatcher
// never exceeds the trigger's batch_size_max in a single tick.
// PEL entries take priority because they've already been
// counted toward the dispatcher's in-flight budget — leaving
// them stranded would amplify the broker-side backlog.
func (r *redisPoller) Poll(ctx context.Context, t sqlc.Trigger) PollResult {
	limit := int64(r.batchMax)
	if t.BatchSizeMax > 0 && int32(limit) > t.BatchSizeMax {
		limit = int64(t.BatchSizeMax)
	}
	out := make([]SourceRecord, 0, limit)
	pelRes, pelErr := r.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    r.group,
		Consumer: r.consumer,
		Streams:  []string{r.stream, "0"},
		Count:    limit,
		Block:    50 * time.Millisecond,
	}).Result()
	if pelErr != nil && !errors.Is(pelErr, redis.Nil) {
		return PollResult{Error: fmt.Errorf("redis_poller: xreadgroup pel: %w", pelErr)}
	}
	r.appendOutFromXReadGroup(out, pelRes)
	newRes, err := r.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    r.group,
		Consumer: r.consumer,
		Streams:  []string{r.stream, ">"},
		Count:    limit - int64(len(out)),
		Block:    250 * time.Millisecond,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return PollResult{Error: fmt.Errorf("redis_poller: xreadgroup: %w", err)}
	}
	r.appendOutFromXReadGroup(out, newRes)
	return PollResult{Records: out}
}

// appendOutFromXReadGroup flattens the XReadGroup result into the
// accumulator slice. Shared by the PEL-drain and new-entry passes
// in Poll (audit #4 split — same body, two callers).
func (r *redisPoller) appendOutFromXReadGroup(out []SourceRecord, res []redis.XStream) []SourceRecord {
	for _, stream := range res {
		if stream.Stream != r.stream {
			continue
		}
		for _, msg := range stream.Messages {
			// Values is map[string]interface{}; flatten into
			// headers + payload. Convention: an entry named
			// "payload" is the JSON body; everything else is a
			// header. If no "payload" key exists, we dump the
			// whole Values map as the body.
			hdrs := map[string]string{}
			payload, hasPayload := msg.Values["payload"]
			for k, v := range msg.Values {
				if k == "payload" {
					continue
				}
				hdrs[k] = fmt.Sprint(v)
			}
			var payloadBytes []byte
			if hasPayload {
				payloadBytes = []byte(fmt.Sprint(payload))
			} else {
				// Marshal the whole map as the payload — better
				// than dropping data.
				b, mErr := json.Marshal(msg.Values)
				if mErr != nil {
					payloadBytes = []byte(fmt.Sprint(payload))
				} else {
					payloadBytes = b
				}
			}
			meta := map[string]any{
				"stream":         r.stream,
				"group":          r.group,
				"delivery_count": msg.DeliveredCount,
				"idle_ms":        msg.MillisElapsedFromDelivery,
			}
			out = append(out, SourceRecord{
				ItemIdentifier: msg.ID,
				Payload:        payloadBytes,
				Headers:        hdrs,
				Metadata:       meta,
				ReceivedAt:     time.Now(),
			})
			// Stash for Ack/Nack bookkeeping.
			r.mu.Lock()
			r.inFlight[msg.ID] = msg.ID
			r.mu.Unlock()
		}
	}
	return out
}

// Ack removes the entries from the consumer's PEL via XAck.
func (r *redisPoller) Ack(ctx context.Context, _ sqlc.Trigger, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := r.client.XAck(ctx, r.stream, r.group, ids...).Err(); err != nil {
		return fmt.Errorf("redis_poller: xack: %w", err)
	}
	r.mu.Lock()
	for _, id := range ids {
		delete(r.inFlight, id)
	}
	r.mu.Unlock()
	return nil
}

// Nack signals broker-side redelivery. Redis Streams doesn't
// have a per-message visibility primitive the way SQS does; the
// closest is XClaim which moves the entry to the calling
// consumer (or anyone in the group) after an idle window.
//
// We use XClaim with r.visibility as the min-idle so a message
// that's been in flight < 30s won't be re-delivered
// immediately. The dispatch tick's trigger_records.next_fire_at
// already encodes the application-level retry delay; this
// 30s window is the broker-side spacing floor.
//
// poison_record: Ack so the entry advances out of PEL. The
// dispatch tick has already recorded the dead-letter row.
func (r *redisPoller) Nack(ctx context.Context, _ sqlc.Trigger, ids []string, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	if reason == triggerReasonPoisonRecord {
		if err := r.client.XAck(ctx, r.stream, r.group, ids...).Err(); err != nil {
			return fmt.Errorf("redis_poller: xack (poison): %w", err)
		}
		r.mu.Lock()
		for _, id := range ids {
			delete(r.inFlight, id)
		}
		r.mu.Unlock()
		return nil
	}
	if _, err := r.client.XClaim(ctx, &redis.XClaimArgs{
		Stream:   r.stream,
		Group:    r.group,
		Consumer: r.consumer,
		MinIdle:  r.visibility,
		Messages: ids,
	}).Result(); err != nil {
		return fmt.Errorf("redis_poller: xclaim: %w", err)
	}
	r.mu.Lock()
	for _, id := range ids {
		delete(r.inFlight, id)
	}
	r.mu.Unlock()
	return nil
}

// Close closes the redis client.
//
// Idempotent (review finding #7): the underlying redis.Client
// uses atomic.CompareAndSwapUint32 on its Close path and
// returns redis.ErrClosed on the second call. schedd can
// hit Close twice on the same instance (trigger delete +
// schedd shutdown unwind race), so we guard behind a
// sync.Once inside the per-instance mutex. The mutex
// ordering is mu, then Once — Close always holds mu until
// the underlying Close() has returned, so concurrent
// first-call Close + parallel cleanup observers can't
// race on the in-flight map.
func (r *redisPoller) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.inFlight {
		delete(r.inFlight, k)
	}
	r.closeOnce.Do(func() {
		r.closeErr = r.client.Close()
	})
	return r.closeErr
}

func init() {
	registerPoller("redis_streams", func(t sqlc.Trigger) (triggerSource, error) {
		return newRedisPoller(t)
	})
}
