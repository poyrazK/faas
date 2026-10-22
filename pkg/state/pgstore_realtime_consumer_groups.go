package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	metricsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type realtimeGroupRow struct {
	deduplicationField  string
	deduplicationWindow int
	circuitEnabled      bool
	priorityField       string
	priorityTurn        int

	filter                    RealtimeConsumerGroupFilter
	claimRate                 int
	deadlineField             string
	retryPolicy               RealtimeConsumerGroupRetryPolicy
	retentionSeconds          int
	expiredDead               string
	orderingField, deadPolicy string
	paused                    bool
	triggerID                 string
	id                        string
	assigned                  int64
	leaseSeconds, maxAttempts int
}

func (s *PgStore) lockRealtimeGroup(ctx context.Context, ep, ch, name string) (pgx.Tx, realtimeGroupRow, error) {
	var g realtimeGroupRow
	if err := validateRealtimeConsumerGroup(ep, ch, name); err != nil {
		return nil, g, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, g, err
	}
	err = tx.QueryRow(ctx, `select id,assigned_sequence,lease_seconds,max_attempts,paused_at is not null,ordering_metadata_key,dead_letter_policy,dead_letter_retention_seconds,expired_dead_letters::text,retry_policy,deadline_metadata_key,claim_rate_per_second,message_filter,coalesce(trigger_id::text,'') from managed_realtime_consumer_groups
 where endpoint_id=$1 and channel=$2 and name=$3 for update`, ep, ch, name).Scan(&g.id, &g.assigned, &g.leaseSeconds, &g.maxAttempts, &g.paused, &g.orderingField, &g.deadPolicy, &g.retentionSeconds, &g.expiredDead, &g.retryPolicy, &g.deadlineField, &g.claimRate, &g.filter, &g.triggerID)
	if err != nil {
		_ = tx.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return nil, g, err
	}
	policy, policyErr := metricsqlc.New().ReadPriorityPolicy(ctx, tx, g.id)
	if policyErr != nil {
		_ = tx.Rollback(ctx)
		return nil, g, policyErr
	}
	deduplication, deduplicationErr := metricsqlc.New().ReadDeduplicationPolicy(ctx, tx, g.id)
	if deduplicationErr != nil {
		_ = tx.Rollback(ctx)
		return nil, g, deduplicationErr
	}
	g.deduplicationField = deduplication.DeduplicationMetadataKey
	g.deduplicationWindow = int(deduplication.DeduplicationWindowSeconds)
	g.priorityField = policy.PriorityMetadataKey
	g.priorityTurn = int(policy.PriorityTurn)
	circuit, circuitErr := readRealtimeCircuit(ctx, tx, g.id)
	if circuitErr != nil {
		_ = tx.Rollback(ctx)
		return nil, g, circuitErr
	}
	g.circuitEnabled = circuit.Enabled
	if err = authorizeRealtimeWorkerTransaction(ctx, tx, g.id); err != nil {
		_ = tx.Rollback(ctx)
		return nil, g, err
	}
	return tx, g, nil
}

// SHARE on the head serializes assignment with history trim/append. The expiry
// floor uses the same highest-expired rule as channel replay.
func realtimeGroupBounds(ctx context.Context, tx pgx.Tx, ep, ch string) (int64, int64, error) {
	var next, oldest int64
	err := tx.QueryRow(ctx, `select next_sequence,oldest_sequence from managed_realtime_channel_heads
 where endpoint_id=$1 and channel=$2 for share`, ep, ch).Scan(&next, &oldest)
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	err = tx.QueryRow(ctx, `select greatest($3::bigint,coalesce(max(sequence)+1,$3::bigint))
 from managed_realtime_channel_messages where endpoint_id=$1 and channel=$2
 and created_at < clock_timestamp()-make_interval(secs=>$4::double precision)`, ep, ch, oldest, ManagedRealtimeHistoryRetention.Seconds()).Scan(&oldest)
	return oldest, next - 1, err
}
func (s *PgStore) CreateRealtimeConsumerGroup(ctx context.Context, ep, ch, name string, cfg RealtimeConsumerGroupConfig) (RealtimeConsumerGroup, error) {
	if err := normalizeRealtimeGroupDeduplication(&cfg); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	filter, err := normalizeRealtimeGroupFilter(cfg.Filter)
	if err != nil {
		return RealtimeConsumerGroup{}, err
	}

	if err := validateRealtimeConsumerGroup(ep, ch, name); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if err := validateRealtimeGroupDeadlineKey(cfg.DeadlineMetadataKey); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if err := validateRealtimeGroupOrdering(&cfg); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if cfg.DeadlineMetadataKey != "" && cfg.OrderingMetadataKey != "" && cfg.DeadLetterPolicy != "skip" {
		return RealtimeConsumerGroup{}, ErrRealtimeConsumerGroupInvalid
	}
	if err := validateRealtimeGroupRetention(cfg.DeadLetterRetentionSeconds, cfg.OrderingMetadataKey, cfg.DeadLetterPolicy); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if err := validateRealtimeGroupDeadlineKey(cfg.PriorityMetadataKey); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	after, err := realtimeGroupSequence(cfg.AfterSequence)
	if err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if cfg.LeaseSeconds == 0 {
		cfg.LeaseSeconds = api.RealtimeConsumerGroupLeaseDefaultSeconds
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = api.RealtimeConsumerGroupAttemptsDefault
	}
	if cfg.LeaseSeconds < 1 || cfg.LeaseSeconds > api.RealtimeConsumerGroupLeaseMaxSeconds || cfg.MaxAttempts < 1 || cfg.MaxAttempts > api.RealtimeConsumerGroupAttemptsMax {
		return RealtimeConsumerGroup{}, ErrRealtimeConsumerGroupInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RealtimeConsumerGroup{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for update`, ep).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RealtimeConsumerGroup{}, ErrNotFound
	}
	if err != nil {
		return RealtimeConsumerGroup{}, err
	}
	var exists bool
	var count int
	err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_consumer_groups where endpoint_id=$1 and channel=$2 and name=$3),
 (select count(*) from managed_realtime_consumer_groups where endpoint_id=$1)`, ep, ch, name).Scan(&exists, &count)
	if err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if exists {
		return RealtimeConsumerGroup{}, ErrConflict
	}
	if count >= api.RealtimeConsumerGroupsMaxPerEndpoint {
		return RealtimeConsumerGroup{}, &RealtimeConsumerGroupCapacityError{Resource: "groups per endpoint", Limit: api.RealtimeConsumerGroupsMaxPerEndpoint, Observed: count}
	}
	oldest, latest, err := realtimeGroupBounds(ctx, tx, ep, ch)
	if err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if after > latest {
		return RealtimeConsumerGroup{}, ErrRealtimeConsumerGroupInvalid
	}
	var created time.Time
	err = tx.QueryRow(ctx, `insert into managed_realtime_consumer_groups(endpoint_id,channel,name,assigned_sequence,lease_seconds,max_attempts,ordering_metadata_key,dead_letter_policy,dead_letter_retention_seconds,deadline_metadata_key)
 values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) returning id,created_at`, ep, ch, name, after, cfg.LeaseSeconds, cfg.MaxAttempts, cfg.OrderingMetadataKey, cfg.DeadLetterPolicy, cfg.DeadLetterRetentionSeconds, cfg.DeadlineMetadataKey).Scan(&id, &created)
	if err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if err = configureRealtimeGroupFilter(ctx, tx, id, filter); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if err = metricsqlc.New().SetInitialPriorityPolicy(ctx, tx, metricsqlc.SetInitialPriorityPolicyParams{GroupID: id, MetadataKey: cfg.PriorityMetadataKey}); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if err = metricsqlc.New().SetInitialDeduplicationPolicy(ctx, tx, metricsqlc.SetInitialDeduplicationPolicyParams{GroupID: id, MetadataKey: cfg.DeduplicationMetadataKey, WindowSeconds: int32(cfg.DeduplicationWindowSeconds)}); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RealtimeConsumerGroup{}, err
	}
	return RealtimeConsumerGroup{DeduplicationMetadataKey: cfg.DeduplicationMetadataKey, DeduplicationWindowSeconds: cfg.DeduplicationWindowSeconds, DuplicateMessages: "0", Filter: filter, FilteredMessages: "0", DeadlineMetadataKey: cfg.DeadlineMetadataKey, ExpiredDeliveries: "0", DeadLetterRetentionSeconds: cfg.DeadLetterRetentionSeconds, ExpiredDeadLetters: "0", LastFencingToken: "0", OrderingMetadataKey: cfg.OrderingMetadataKey, DeadLetterPolicy: cfg.DeadLetterPolicy, GroupID: id, Name: name, Channel: ch, AssignedSequence: strconv.FormatInt(after, 10), LatestSequence: strconv.FormatInt(latest, 10), OldestSequence: strconv.FormatInt(oldest, 10), Unassigned: strconv.FormatInt(latest-after, 10), Completed: "0", HistoryUnavailable: after < oldest-1, LeaseSeconds: cfg.LeaseSeconds, MaxAttempts: cfg.MaxAttempts, CreatedAt: created}, nil
}
func (s *PgStore) ListRealtimeConsumerGroups(ctx context.Context, ep, ch string) ([]RealtimeConsumerGroup, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return nil, ErrRealtimeConsumerGroupInvalid
	}
	var tx pgx.Tx
	var err error
	var scopedGroupID *string
	if worker, ok := realtimeWorkerFromContext(ctx); ok {
		if err = requireRealtimeWorkerOperation(ctx, "status"); err != nil {
			return nil, err
		}
		if worker.grant.EndpointID != ep || worker.grant.Channel != ch {
			return nil, ErrRealtimeWorkerCredentialDenied
		}
		tx, _, err = s.lockRealtimeGroup(ctx, ep, ch, worker.grant.Group)
		scopedGroupID = &worker.grant.Info.GroupID
	} else {
		tx, err = s.pool.Begin(ctx)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	oldest, latest, err := realtimeGroupBounds(ctx, tx, ep, ch)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select g.id,g.name,g.assigned_sequence,g.completed::text,g.lease_seconds,g.max_attempts,g.created_at,g.paused_at,g.draining,g.ordering_metadata_key,g.dead_letter_policy,g.last_fencing_token::text,g.dead_letter_retention_seconds,g.expired_dead_letters::text,g.deadline_metadata_key,g.expired_deliveries::text,g.message_filter,g.filtered_messages::text,coalesce(g.trigger_id::text,''),
 count(m.sequence) filter(where m.status='ready'),count(m.sequence) filter(where m.status='leased' and m.lease_until>clock_timestamp()),
 count(m.sequence) filter(where m.status='leased' and m.lease_until<=clock_timestamp()),count(m.sequence) filter(where m.status='dead'),
 count(distinct m.worker) filter(where m.status='leased' and m.lease_until>clock_timestamp()),
 count(m.sequence) filter(where m.status='ready' and g.ordering_metadata_key<>'' and exists(select 1 from managed_realtime_consumer_group_messages b where b.group_id=g.id and b.ordering_key=m.ordering_key and (b.status='leased' or (b.sequence<m.sequence and (b.status<>'dead' or g.dead_letter_policy='block')))))
 from managed_realtime_consumer_groups g left join managed_realtime_consumer_group_messages m on m.group_id=g.id
 where g.endpoint_id=$1 and g.channel=$2 and ($4::uuid is null or g.id=$4) group by g.id order by g.name limit $3`, ep, ch, api.RealtimeConsumerGroupsMaxPerEndpoint, scopedGroupID)
	if err != nil {
		return nil, err
	}
	result := []RealtimeConsumerGroup{}
	for rows.Next() {
		var g RealtimeConsumerGroup
		var assigned int64
		if err = rows.Scan(&g.GroupID, &g.Name, &assigned, &g.Completed, &g.LeaseSeconds, &g.MaxAttempts, &g.CreatedAt, &g.PausedAt, &g.Draining, &g.OrderingMetadataKey, &g.DeadLetterPolicy, &g.LastFencingToken, &g.DeadLetterRetentionSeconds, &g.ExpiredDeadLetters, &g.DeadlineMetadataKey, &g.ExpiredDeliveries, &g.Filter, &g.FilteredMessages, &g.TriggerID, &g.Ready, &g.Leased, &g.ExpiredLeases, &g.Dead, &g.ActiveWorkers, &g.OrderingBlocked); err != nil {
			rows.Close()
			return nil, err
		}
		g.Paused = g.PausedAt != nil
		g.Drained = g.Draining && g.Leased == 0
		g.Channel = ch
		g.AssignedSequence = strconv.FormatInt(assigned, 10)
		g.LatestSequence = strconv.FormatInt(latest, 10)
		g.OldestSequence = strconv.FormatInt(oldest, 10)
		lag := latest - assigned
		if lag < 0 {
			lag = 0
		}
		g.Unassigned = strconv.FormatInt(lag, 10)
		g.HistoryUnavailable = assigned < oldest-1 || assigned > latest
		g.CapacityBlocked = g.Ready+g.Leased+g.ExpiredLeases+g.Dead >= api.RealtimeConsumerGroupOutstandingMax || g.Leased >= api.RealtimeConsumerGroupLeasesMax
		result = append(result, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = loadRealtimeGroupDeduplicationStatuses(ctx, tx, result); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *PgStore) DeleteRealtimeConsumerGroup(ctx context.Context, ep, ch, name string) error {
	if err := requireRealtimeWorkerOperation(ctx, "delete"); err != nil {
		return err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `delete from managed_realtime_consumer_groups where id=$1`, g.id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func expireRealtimeGroupLeases(ctx context.Context, tx pgx.Tx, g realtimeGroupRow) error {
	if err := expireRealtimeGroupDeadlines(ctx, tx, g); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `update managed_realtime_consumer_group_messages set
 status=case when attempts>=$2 then 'dead' else 'ready' end,worker='',token_hash=null,lease_until=null,
 failure_code='lease_expired',credential_id=null,available_at=clock_timestamp()
 where group_id=$1 and status='leased' and lease_until<=clock_timestamp()`, g.id, g.maxAttempts)
	return err
}
func realtimeGroupCounts(ctx context.Context, tx pgx.Tx, id string) (int, int, error) {
	var total, leased int
	err := tx.QueryRow(ctx, `select count(*),count(*) filter(where status='leased') from managed_realtime_consumer_group_messages where group_id=$1`, id).Scan(&total, &leased)
	return total, leased, err
}
func (s *PgStore) ClaimRealtimeConsumerGroup(ctx context.Context, ep, ch, name, worker string) (*RealtimeConsumerGroupClaim, error) {
	batch, err := s.ClaimRealtimeConsumerGroupBatch(ctx, ep, ch, name, worker, 1)
	if err != nil {
		return nil, err
	}
	if len(batch.Claims) != 0 {
		return &batch.Claims[0], nil
	}
	if batch.DeduplicationBlocked {
		return nil, &RealtimeConsumerGroupDeduplicationFullError{RetryAfterMilliseconds: batch.RetryAfterMilliseconds}
	}
	if batch.CircuitOpen {
		return nil, &RealtimeConsumerGroupCircuitOpenError{RetryAfterMilliseconds: batch.RetryAfterMilliseconds}
	}
	if batch.RateThrottled {
		return nil, &RealtimeConsumerGroupRateLimitError{RetryAfterMilliseconds: batch.RetryAfterMilliseconds}
	}
	if batch.HistoryUnavailable {
		return nil, ErrRealtimeConsumerGroupGap
	}
	if batch.CapacityBlocked {
		return nil, &RealtimeConsumerGroupCapacityError{Resource: "outstanding messages per group", Limit: api.RealtimeConsumerGroupOutstandingMax, Observed: batch.outstanding}
	}
	return nil, nil
}
func (s *PgStore) ClaimRealtimeConsumerGroupBatch(ctx context.Context, ep, ch, name, worker string, limit int) (RealtimeConsumerGroupBatch, error) {
	result := RealtimeConsumerGroupBatch{Claims: []RealtimeConsumerGroupClaim{}}
	if err := requireRealtimeWorkerOperation(ctx, "claim"); err != nil {
		return result, err
	}
	if !validRealtimeWorkerName(worker, api.RealtimeConsumerGroupWorkerMaxBytes) || limit < 1 || limit > api.RealtimeConsumerGroupBatchMax {
		return result, ErrRealtimeConsumerGroupInvalid
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = requireRealtimeGroupConsumer(ctx, g); err != nil {
		return result, err
	}
	if err = expireRealtimeGroupLeases(ctx, tx, g); err != nil {
		return result, err
	}
	if g.paused {
		result.Paused = true
		return result, tx.Commit(ctx)
	}
	total, leased, err := realtimeGroupCounts(ctx, tx, g.id)
	if err != nil {
		return result, err
	}
	limit = min(limit, api.RealtimeConsumerGroupLeasesMax-leased)
	scanned := 0
	for len(result.Claims) < limit {
		circuit, err := gateRealtimeCircuit(ctx, tx, g)
		if err != nil {
			return RealtimeConsumerGroupBatch{}, err
		}
		if circuit.Blocked {
			result.CircuitOpen = true
			result.RetryAfterMilliseconds = circuit.RetryAfterMilliseconds
			break
		}

		if g.claimRate > 0 {
			retry, err := gateRealtimeClaimRate(ctx, tx, g.id)
			if err != nil {
				return RealtimeConsumerGroupBatch{}, err
			}
			if retry > 0 {
				result.RateThrottled = true
				result.RetryAfterMilliseconds = retry
				break
			}
		}
		sequence, err := nextRealtimeGroupBatchMessage(ctx, tx, &g, ep, ch, &total, &scanned)
		var deduplicationFull *RealtimeConsumerGroupDeduplicationFullError
		if errors.As(err, &deduplicationFull) {
			result.DeduplicationBlocked = true
			result.RetryAfterMilliseconds = deduplicationFull.RetryAfterMilliseconds
			break
		}
		if errors.Is(err, ErrRealtimeConsumerGroupFilterScanLimited) {
			result.FilterScanLimited = true
			break
		}
		if errors.Is(err, ErrRealtimeConsumerGroupOrderingBlocked) {
			result.OrderingBlocked = true
			break
		}
		if errors.Is(err, ErrRealtimeConsumerGroupGap) {
			result.HistoryUnavailable = true
			break
		}
		if errors.Is(err, ErrRealtimeConsumerGroupLimit) {
			result.CapacityBlocked = true
			result.outstanding = total
			break
		}
		if err != nil {
			return RealtimeConsumerGroupBatch{}, err
		}
		if sequence == 0 {
			break
		}
		claim, err := leaseRealtimeGroupMessage(ctx, tx, g, sequence, worker)
		if errors.Is(err, pgx.ErrNoRows) {
			if err = expireRealtimeGroupDeadlines(ctx, tx, g); err != nil {
				return RealtimeConsumerGroupBatch{}, err
			}
			continue
		}
		if err != nil {
			return RealtimeConsumerGroupBatch{}, err
		}
		if circuit.State == "half_open" {
			if err = metricsqlc.New().RegisterCircuitProbe(ctx, tx, metricsqlc.RegisterCircuitProbeParams{GroupID: g.id, Sequence: sequence, FencingToken: claim.FencingToken}); err != nil {
				return RealtimeConsumerGroupBatch{}, err
			}
		}
		if g.claimRate > 0 {
			if err = advanceRealtimeClaimRate(ctx, tx, g.id); err != nil {
				return RealtimeConsumerGroupBatch{}, err
			}
		}
		if g.priorityField != "" {
			if err = metricsqlc.New().AdvancePriorityTurn(ctx, tx, metricsqlc.AdvancePriorityTurnParams{GroupID: g.id, FairTurns: api.RealtimeConsumerGroupPriorityFairTurns}); err != nil {
				return RealtimeConsumerGroupBatch{}, err
			}
			g.priorityTurn = (g.priorityTurn + 1) % api.RealtimeConsumerGroupPriorityFairTurns
		}
		result.Claims = append(result.Claims, *claim)
	}
	if err = tx.Commit(ctx); err != nil {
		return RealtimeConsumerGroupBatch{}, err
	}
	return result, nil
}
func nextRealtimeGroupBatchMessage(ctx context.Context, tx pgx.Tx, g *realtimeGroupRow, ep, ch string, total *int, scanned *int) (int64, error) {
	for {
		if g.priorityField != "" {
			if err := prefillRealtimePriorityWindow(ctx, tx, g, ep, ch, total, scanned); err != nil {
				return 0, err
			}
		}
		sequence, err := metricsqlc.New().NextPriorityMessage(ctx, tx, metricsqlc.NextPriorityMessageParams{GroupID: g.id, Ordered: g.orderingField != "", DeadPolicy: g.deadPolicy, PreferPriority: g.priorityField != "" && g.priorityTurn != api.RealtimeConsumerGroupPriorityFairTurns-1})
		if err == nil {
			return sequence, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		if *total >= api.RealtimeConsumerGroupOutstandingMax {
			if blocked, err := realtimeGroupOrderingBlocked(ctx, tx, *g); err != nil {
				return 0, err
			} else if blocked {
				return 0, ErrRealtimeConsumerGroupOrderingBlocked
			}
			return 0, ErrRealtimeConsumerGroupLimit
		}
		if *scanned >= api.RealtimeConsumerGroupFilterScanMax {
			return 0, ErrRealtimeConsumerGroupFilterScanLimited
		}
		var assigned bool
		sequence, assigned, err = assignRealtimeGroupMessage(ctx, tx, *g, ep, ch)
		*scanned++
		if err != nil {
			return 0, err
		}
		if sequence == 0 {
			if blocked, err := realtimeGroupOrderingBlocked(ctx, tx, *g); err != nil {
				return 0, err
			} else if blocked {
				return 0, ErrRealtimeConsumerGroupOrderingBlocked
			}
			return 0, nil
		}
		if assigned {
			*total++
		}
		g.assigned = sequence
	}
}
func realtimeGroupOrderingBlocked(ctx context.Context, tx pgx.Tx, g realtimeGroupRow) (bool, error) {
	if g.orderingField == "" {
		return false, nil
	}
	var blocked bool
	err := tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_consumer_group_messages m
 where m.group_id=$1 and m.status='ready' and exists(select 1 from managed_realtime_consumer_group_messages b
 where b.group_id=m.group_id and b.ordering_key=m.ordering_key
 and (b.status='leased' or (b.sequence<m.sequence and (b.status<>'dead' or $2='block')))))`, g.id, g.deadPolicy).Scan(&blocked)
	return blocked, err
}
func assignRealtimeGroupMessage(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, ep, ch string) (int64, bool, error) {
	oldest, latest, err := realtimeGroupBounds(ctx, tx, ep, ch)
	if err != nil {
		return 0, false, err
	}
	if g.assigned < oldest-1 || g.assigned > latest {
		return 0, false, ErrRealtimeConsumerGroupGap
	}
	if g.assigned == latest {
		return 0, false, nil
	}
	msg := ManagedRealtimeChannelMessage{EndpointID: ep, Channel: ch}
	err = tx.QueryRow(ctx, `select sequence,data,is_binary,metadata,target_message_id,version,message_event,deleted,created_at
 from managed_realtime_channel_messages where endpoint_id=$1 and channel=$2 and sequence=$3`, ep, ch, g.assigned+1).Scan(&msg.Sequence, &msg.Data, &msg.Binary, &msg.Metadata, &msg.TargetMessageID, &msg.Version, &msg.MessageEvent, &msg.Deleted, &msg.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrRealtimeConsumerGroupGap
	}
	if err != nil {
		return 0, false, err
	}
	if !realtimeGroupFilterMatches(g.filter, msg.Metadata) {
		err = skipRealtimeGroupFilteredMessage(ctx, tx, g.id, msg.Sequence)
		return msg.Sequence, false, err
	}
	duplicate, err := skipRealtimeGroupDuplicate(ctx, tx, g, msg.Sequence, msg.Metadata)
	if err != nil || duplicate {
		return msg.Sequence, false, err
	}
	payload, err := json.Marshal(realtimeGroupMessage(msg))
	if err != nil {
		return 0, false, err
	}
	_, err = tx.Exec(ctx, `insert into managed_realtime_consumer_group_messages(group_id,sequence,message,status,ordering_key,ordered) values($1,$2,$3::jsonb,'ready',$4,$5)`, g.id, msg.Sequence, payload, msg.Metadata[g.orderingField], g.orderingField != "")
	if err != nil {
		return 0, false, err
	}
	if g.priorityField != "" {
		if err = freezeRealtimeGroupPriority(ctx, tx, g, msg.Sequence, msg.Metadata); err != nil {
			return 0, false, err
		}
	}
	if err = freezeRealtimeGroupDeadline(ctx, tx, g, msg.Sequence, msg.Metadata); err != nil {
		return 0, false, err
	}
	if err = expireRealtimeGroupDeadlines(ctx, tx, g); err != nil {
		return 0, false, err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_consumer_groups set assigned_sequence=$2 where id=$1`, g.id, msg.Sequence)
	return msg.Sequence, true, err
}
func leaseRealtimeGroupMessage(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, seq int64, worker string) (*RealtimeConsumerGroupClaim, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(token)
	var credentialID *string
	if grant, ok := realtimeWorkerFromContext(ctx); ok {
		credentialID = &grant.grant.Info.ID
	}
	result := &RealtimeConsumerGroupClaim{GroupID: g.id, Worker: worker, ClaimToken: base64.RawURLEncoding.EncodeToString(token)}
	var payload []byte
	err := tx.QueryRow(ctx, `update managed_realtime_consumer_groups set last_fencing_token=last_fencing_token+1
 where id=$1 and last_fencing_token<$2 returning last_fencing_token::text`, g.id, api.RealtimeConsumerGroupFencingMax).Scan(&result.FencingToken)
	if errors.Is(err, pgx.ErrNoRows) {
		ceiling := api.RealtimeConsumerGroupFencingMax
		return nil, &RealtimeConsumerGroupCapacityError{Resource: "group fencing counter", Limit: int(ceiling), Observed: int(ceiling)}
	}
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `update managed_realtime_consumer_group_messages set status='leased',attempts=attempts+1,
 worker=$3,token_hash=$4,credential_id=$6,fencing_token=$7::bigint,lease_until=least(clock_timestamp()+make_interval(secs=>$5::double precision),delivery_deadline)
 where group_id=$1 and sequence=$2 and status='ready' and not deadline_invalid and (delivery_deadline is null or delivery_deadline>clock_timestamp()) returning message,attempts,lease_until,delivery_deadline`, g.id, seq, worker, hash[:], g.leaseSeconds, credentialID, result.FencingToken).Scan(&payload, &result.Attempt, &result.LeaseUntil, &result.DeliveryDeadline)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(payload, &result.Message); err != nil {
		return nil, err
	}
	return result, nil
}
func validateRealtimeGroupSettlement(req RealtimeConsumerGroupSettlement) (int64, []byte, error) {
	if err := validateRealtimeSettlementKey(req.IdempotencyKey); err != nil {
		return 0, nil, err
	}
	seq, err := realtimeGroupSequence(req.Sequence)
	if err != nil || seq == 0 {
		return 0, nil, ErrRealtimeConsumerGroupInvalid
	}
	token, err := base64.RawURLEncoding.DecodeString(req.ClaimToken)
	if err != nil || len(token) != 32 {
		return 0, nil, ErrRealtimeConsumerGroupInvalid
	}
	if req.Action != "complete" && req.Action != "release" && req.Action != "retry" && req.Action != "renew" {
		return 0, nil, ErrRealtimeConsumerGroupInvalid
	}
	if (req.RetryAfterSeconds != nil && (*req.RetryAfterSeconds < 0 || *req.RetryAfterSeconds > api.RealtimeConsumerGroupRetryMaxSeconds)) || len(req.FailureCode) > api.RealtimeConsumerGroupFailureMaxBytes {
		return 0, nil, ErrRealtimeConsumerGroupInvalid
	}
	if req.Action != "retry" && (req.RetryAfterSeconds != nil || req.FailureCode != "") {
		return 0, nil, ErrRealtimeConsumerGroupInvalid
	}
	for _, r := range req.FailureCode {
		if r < 32 || r > 126 {
			return 0, nil, ErrRealtimeConsumerGroupInvalid
		}
	}
	hash := sha256.Sum256(token)
	return seq, hash[:], nil
}
func (s *PgStore) SettleRealtimeConsumerGroup(ctx context.Context, ep, ch, name string, req RealtimeConsumerGroupSettlement) (RealtimeConsumerGroupSettlementResult, error) {
	if err := requireRealtimeWorkerOperation(ctx, req.Action); err != nil {
		return RealtimeConsumerGroupSettlementResult{}, err
	}

	var result RealtimeConsumerGroupSettlementResult
	seq, hash, err := validateRealtimeGroupSettlement(req)
	if err != nil {
		return result, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = requireRealtimeGroupConsumer(ctx, g); err != nil {
		return result, err
	}
	receipt, found, err := prepareRealtimeSettlementReceipt(ctx, tx, g.id, req.IdempotencyKey, "single", []realtimeBatchSettlementInput{{request: req, sequence: seq, hash: hash}}, &result)
	if err != nil {
		return result, err
	}
	if found {
		return result, tx.Commit(ctx)
	}
	attempts, err := readRealtimeSettlementAttempts(ctx, tx, g.id, seq, hash)
	if err != nil {
		return result, err
	}
	result, err = settleRealtimeGroupMessage(ctx, tx, g, seq, hash, attempts, req)
	if err != nil {
		return result, err
	}
	if err = saveRealtimeSettlementReceipt(ctx, tx, receipt, result); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}
func settleRealtimeGroupMessage(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, seq int64, hash []byte, attempts int, req RealtimeConsumerGroupSettlement) (RealtimeConsumerGroupSettlementResult, error) {
	var result RealtimeConsumerGroupSettlementResult
	var err error
	switch req.Action {
	case "complete":
		_, err = metricsqlc.New().CompleteMeasuredMessage(ctx, tx, metricsqlc.CompleteMeasuredMessageParams{GroupID: g.id, Sequence: seq, TokenHash: hash})
		if err == nil {
			_, err = tx.Exec(ctx, `update managed_realtime_consumer_groups set completed=completed+1 where id=$1`, g.id)
		}
		result.Status = "completed"
	case "release":
		available, releaseErr := metricsqlc.New().ReleaseGroupMessage(ctx, tx, metricsqlc.ReleaseGroupMessageParams{GroupID: g.id, Sequence: seq, TokenHash: hash})
		err = releaseErr
		result.Status = "ready"
		if err == nil {
			result.AvailableAt = &available.Time
		}
	case "renew":
		var until time.Time
		err = tx.QueryRow(ctx, `update managed_realtime_consumer_group_messages set lease_until=least(clock_timestamp()+make_interval(secs=>$4::double precision),delivery_deadline)
 where group_id=$1 and sequence=$2 and token_hash=$3 and lease_until>clock_timestamp() returning lease_until`, g.id, seq, hash, g.leaseSeconds).Scan(&until)
		result.Status = "leased"
		result.LeaseUntil = &until
	case "retry":
		result.Status = "ready"
		if attempts >= g.maxAttempts {
			result.Status = "dead"
		}
		code := req.FailureCode
		if code == "" {
			code = "handler_failed"
		}
		delay := 0
		if result.Status != "dead" {
			var permanent bool
			var decisionErr error
			delay, permanent, decisionErr = realtimeRetryDecision(g.retryPolicy, attempts, code, req.RetryAfterSeconds)
			if decisionErr != nil {
				return result, decisionErr
			}
			if permanent {
				result.Status = "dead"
			}
		}
		var available time.Time
		err = tx.QueryRow(ctx, `update managed_realtime_consumer_group_messages set status=$4,worker='',token_hash=null,lease_until=null,credential_id=null,
 failure_code=$5,available_at=clock_timestamp()+make_interval(secs=>$6::double precision)
 where group_id=$1 and sequence=$2 and token_hash=$3 and lease_until>clock_timestamp() returning available_at`, g.id, seq, hash, result.Status, code, delay).Scan(&available)
		if result.Status == "ready" && err == nil {
			result.AvailableAt = &available
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrRealtimeConsumerGroupLease
	}
	return result, err
}
func (s *PgStore) ListRealtimeConsumerGroupDeadLetters(ctx context.Context, ep, ch, name string) ([]RealtimeConsumerGroupDeadLetter, error) {
	if err := requireRealtimeWorkerOperation(ctx, "dead_letters"); err != nil {
		return nil, err
	}

	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `select message,attempts,case when status='dead' then failure_code else 'lease_expired' end,dead_since,
 case when $4>0 then dead_since+make_interval(secs=>$4::double precision) end,expiry_warned_at,
 case when $4>0 and expiry_warned_at is not null then greatest(dead_since+make_interval(secs=>$4::double precision),expiry_warned_at+make_interval(secs=>$5::double precision)) end
 from managed_realtime_consumer_group_messages where group_id=$1 and (status='dead' or (status='leased' and lease_until<=clock_timestamp() and attempts>=$2))
 order by sequence limit $3`, g.id, g.maxAttempts, api.RealtimeConsumerGroupOutstandingMax, g.retentionSeconds, api.RealtimeConsumerGroupDeadWarningSeconds)
	if err != nil {
		return nil, err
	}
	result := []RealtimeConsumerGroupDeadLetter{}
	for rows.Next() {
		var d RealtimeConsumerGroupDeadLetter
		var payload []byte
		if err = rows.Scan(&payload, &d.Attempts, &d.FailureCode, &d.RetentionStartedAt, &d.ExpiresAt, &d.WarningAt, &d.CleanupAfter); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(payload, &d.Message); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
func (s *PgStore) ReplayRealtimeConsumerGroupDeadLetter(ctx context.Context, ep, ch, name, sequence string) error {
	if err := requireRealtimeWorkerOperation(ctx, "replay"); err != nil {
		return err
	}

	seq, err := realtimeGroupSequence(sequence)
	if err != nil || seq == 0 {
		return ErrRealtimeConsumerGroupInvalid
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = expireRealtimeGroupLeases(ctx, tx, g); err != nil {
		return err
	}
	rejected, err := realtimeGroupReplayDeadline(ctx, tx, g, seq)
	if err != nil {
		return err
	}
	if rejected {
		return ErrConflict
	}

	if g.orderingField != "" {
		var active bool
		err = tx.QueryRow(ctx, `select exists(select 1 from managed_realtime_consumer_group_messages m join managed_realtime_consumer_group_messages b
 on b.group_id=m.group_id and b.ordering_key=m.ordering_key where m.group_id=$1 and m.sequence=$2 and b.status='leased')`, g.id, seq).Scan(&active)
		if err != nil {
			return err
		}
		if active {
			return ErrConflict
		}
	}
	tag, err := tx.Exec(ctx, `update managed_realtime_consumer_group_messages set status='ready',attempts=0,worker='',token_hash=null,lease_until=null,
 failure_code='',credential_id=null,available_at=clock_timestamp() where group_id=$1 and sequence=$2 and status='dead'`, g.id, seq)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
func (s *PgStore) ResetRealtimeConsumerGroup(ctx context.Context, ep, ch, name, expected, value string) error {
	if err := requireRealtimeWorkerOperation(ctx, "resync"); err != nil {
		return err
	}

	before, err := realtimeGroupSequence(expected)
	if err != nil {
		return err
	}
	after, err := realtimeGroupSequence(value)
	if err != nil {
		return err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if before != g.assigned {
		return ErrConflict
	}
	if err = expireRealtimeGroupLeases(ctx, tx, g); err != nil {
		return err
	}
	var pending int
	err = tx.QueryRow(ctx, `select count(*) from managed_realtime_consumer_group_messages where group_id=$1 and status<>'dead'`, g.id).Scan(&pending)
	if err != nil {
		return err
	}
	if pending != 0 {
		return ErrConflict
	}
	oldest, _, err := realtimeGroupBounds(ctx, tx, ep, ch)
	if err != nil {
		return err
	}
	if after != oldest-1 || after <= g.assigned {
		return ErrRealtimeConsumerGroupInvalid
	}
	_, err = tx.Exec(ctx, `update managed_realtime_consumer_groups set assigned_sequence=$2 where id=$1`, g.id, after)
	if err != nil {
		return fmt.Errorf("state: reset realtime consumer group: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) SetRealtimeConsumerGroupPaused(ctx context.Context, ep, ch, name string, paused bool) (RealtimeConsumerGroupPauseState, error) {
	var result RealtimeConsumerGroupPauseState
	operation := "resume"
	if paused {
		operation = "pause"
	}
	if err := requireRealtimeWorkerOperation(ctx, operation); err != nil {
		return result, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `update managed_realtime_consumer_groups
 set draining=false,paused_at=case when $2 then coalesce(paused_at,clock_timestamp()) else null end
 where id=$1 returning paused_at`, g.id, paused).Scan(&result.PausedAt)
	if err != nil {
		return result, err
	}
	result.Paused = result.PausedAt != nil
	return result, tx.Commit(ctx)
}

func (s *PgStore) DrainRealtimeConsumerGroup(ctx context.Context, ep, ch, name string) (RealtimeConsumerGroupDrainState, error) {
	var result RealtimeConsumerGroupDrainState
	if err := requireRealtimeWorkerOperation(ctx, "drain"); err != nil {
		return result, err
	}
	tx, g, err := s.lockRealtimeGroup(ctx, ep, ch, name)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `update managed_realtime_consumer_groups
 set draining=true,paused_at=coalesce(paused_at,clock_timestamp()) where id=$1 returning paused_at`, g.id).Scan(&result.PausedAt)
	if err != nil {
		return result, err
	}
	err = tx.QueryRow(ctx, `select count(*) from managed_realtime_consumer_group_messages
 where group_id=$1 and status='leased' and lease_until>clock_timestamp()`, g.id).Scan(&result.ActiveLeases)
	if err != nil {
		return result, err
	}
	result.Draining = true
	result.Drained = result.ActiveLeases == 0
	return result, tx.Commit(ctx)
}
