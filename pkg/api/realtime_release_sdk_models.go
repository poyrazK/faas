// Code generated from the managed realtime OpenAPI schemas. DO NOT EDIT.
package api

import (
	"time"
)

type RealtimeReplayJobWebhookPayload struct {
	EventID        string    `json:"event_id"`
	AppID          string    `json:"app_id"`
	EndpointID     string    `json:"endpoint_id"`
	GroupID        string    `json:"group_id"`
	JobID          string    `json:"job_id"`
	Channel        string    `json:"channel"`
	Group          string    `json:"group"`
	State          string    `json:"state"`
	Rate           int       `json:"rate"`
	Selected       int       `json:"selected"`
	Processed      int       `json:"processed"`
	Replayed       int       `json:"replayed"`
	Skipped        int       `json:"skipped"`
	Remaining      int       `json:"remaining"`
	ErrorCount     int64     `json:"error_count"`
	ErrorCode      string    `json:"error_code"`
	ErrorThreshold int       `json:"error_threshold"`
	InspectorPath  string    `json:"inspector_path"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type RealtimeDeadLetterExportRecord struct {
	Type             string                       `json:"type"`
	GroupID          string                       `json:"group_id"`
	Sequence         string                       `json:"sequence"`
	DeadSince        time.Time                    `json:"dead_since"`
	Attempts         int                          `json:"attempts"`
	FailureCode      string                       `json:"failure_code"`
	FencingToken     *string                      `json:"fencing_token"`
	DeliveryDeadline *time.Time                   `json:"delivery_deadline"`
	DeadlineInvalid  bool                         `json:"deadline_invalid"`
	OrderingKey      string                       `json:"ordering_key"`
	Priority         int                          `json:"priority"`
	ExpiresAt        *time.Time                   `json:"expires_at"`
	Message          RealtimeConsumerGroupMessage `json:"message"`
}

type RealtimeConsumerGroupMessage struct {
	Sequence        string            `json:"sequence"`
	DataBase64      string            `json:"data_base64"`
	Binary          bool              `json:"binary"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	TargetMessageID *string           `json:"target_message_id,omitempty"`
	Version         int64             `json:"version"`
	Event           string            `json:"event"`
	Deleted         bool              `json:"deleted"`
	CreatedAt       time.Time         `json:"created_at"`
}

type RealtimeDeadLetterExportManifest struct {
	Type              string    `json:"type"`
	FormatVersion     int       `json:"format_version"`
	GroupID           string    `json:"group_id"`
	Group             string    `json:"group"`
	Channel           string    `json:"channel"`
	ExportedAt        time.Time `json:"exported_at"`
	SnapshotAt        time.Time `json:"snapshot_at"`
	AfterSequence     string    `json:"after_sequence"`
	ThroughSequence   string    `json:"through_sequence"`
	FailureCodes      []string  `json:"failure_codes"`
	Count             int       `json:"count"`
	HasMore           bool      `json:"has_more"`
	NextAfterSequence string    `json:"next_after_sequence"`
}

type RealtimeChannelWebhookFilter struct {
	EndpointID string            `json:"endpoint_id"`
	Channel    string            `json:"channel"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type RealtimeAcknowledgement struct {
	CheckpointSequence   string     `json:"checkpoint_sequence"`
	AcknowledgedSequence *string    `json:"acknowledged_sequence"`
	AcknowledgedAt       *time.Time `json:"acknowledged_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type RealtimeDeadLetterBulkReplayResult struct {
	GroupID  string                            `json:"group_id"`
	Replayed int                               `json:"replayed"`
	Outcomes []RealtimeDeadLetterReplayOutcome `json:"outcomes"`
}

type RealtimeDeadLetterReplayOutcome struct {
	Sequence string `json:"sequence"`
	Status   string `json:"status"`
}

type RealtimeDeadLetterBulkReplayRequest struct {
	GroupID string                              `json:"group_id"`
	Items   []RealtimeDeadLetterReplaySelection `json:"items"`
}

type RealtimeDeadLetterReplaySelection struct {
	Sequence  string    `json:"sequence"`
	DeadSince time.Time `json:"dead_since"`
}

type RealtimeReplayJobRequest struct {
	Rate            int      `json:"rate"`
	FailureCodes    []string `json:"failure_codes,omitempty"`
	AfterSequence   *string  `json:"after_sequence,omitempty"`
	ThroughSequence *string  `json:"through_sequence,omitempty"`
}

type RealtimeReplayJob struct {
	ID            string                     `json:"id"`
	GroupID       string                     `json:"group_id"`
	State         string                     `json:"state"`
	Rate          int                        `json:"rate"`
	Selected      int                        `json:"selected"`
	Processed     int                        `json:"processed"`
	Replayed      int                        `json:"replayed"`
	Skipped       int                        `json:"skipped"`
	Outcomes      []RealtimeReplayJobOutcome `json:"outcomes"`
	CreatedAt     time.Time                  `json:"created_at"`
	UpdatedAt     time.Time                  `json:"updated_at"`
	ErrorCount    int64                      `json:"error_count"`
	LastError     string                     `json:"last_error"`
	DryRun        bool                       `json:"dry_run"`
	Eligible      int                        `json:"eligible"`
	ReviewedJobID *string                    `json:"reviewed_job_id"`
	StartedJobID  *string                    `json:"started_job_id"`
}

type RealtimeReplayJobOutcome struct {
	Sequence  string     `json:"sequence"`
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

type RealtimeDeadLetterPreview struct {
	GroupID string                          `json:"group_id"`
	Items   []RealtimeDeadLetterPreviewItem `json:"items"`
	HasMore bool                            `json:"has_more"`
}

type RealtimeDeadLetterPreviewItem struct {
	Sequence        string     `json:"sequence"`
	DeadSince       time.Time  `json:"dead_since"`
	Attempts        int        `json:"attempts"`
	FailureCode     string     `json:"failure_code"`
	OrderingBlocked bool       `json:"ordering_blocked"`
	ExpiresAt       *time.Time `json:"expires_at"`
}

type RealtimeDeadLetterPreviewRequest struct {
	FailureCodes    []string `json:"failure_codes,omitempty"`
	AfterSequence   *string  `json:"after_sequence,omitempty"`
	ThroughSequence *string  `json:"through_sequence,omitempty"`
	Limit           *int     `json:"limit,omitempty"`
}

type RealtimeConsumerGroupClaimRequest struct {
	Worker      string `json:"worker"`
	WaitSeconds *int   `json:"wait_seconds,omitempty"`
}

type RealtimeConsumerGroupCreateRequest struct {
	DeduplicationMetadataKey   *string                      `json:"deduplication_metadata_key,omitempty"`
	DeduplicationWindowSeconds *int                         `json:"deduplication_window_seconds,omitempty"`
	Name                       string                       `json:"name"`
	AfterSequence              string                       `json:"after_sequence"`
	LeaseSeconds               *int                         `json:"lease_seconds,omitempty"`
	MaxAttempts                *int                         `json:"max_attempts,omitempty"`
	OrderingMetadataKey        *string                      `json:"ordering_metadata_key,omitempty"`
	DeadLetterPolicy           *string                      `json:"dead_letter_policy,omitempty"`
	DeadLetterRetentionSeconds *int                         `json:"dead_letter_retention_seconds,omitempty"`
	PriorityMetadataKey        *string                      `json:"priority_metadata_key,omitempty"`
	DeadlineMetadataKey        *string                      `json:"deadline_metadata_key,omitempty"`
	Filter                     *RealtimeConsumerGroupFilter `json:"filter,omitempty"`
}

type RealtimeConsumerGroupFilter struct {
	EventTypes []string          `json:"event_types,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type RealtimeConsumerGroupResyncRequest struct {
	ExpectedSequence string `json:"expected_sequence"`
	AfterSequence    string `json:"after_sequence"`
}

type RealtimeConsumerGroupDeadLetter struct {
	Message            RealtimeConsumerGroupMessage `json:"message"`
	Attempts           int                          `json:"attempts"`
	FailureCode        string                       `json:"failure_code"`
	RetentionStartedAt *time.Time                   `json:"retention_started_at"`
	ExpiresAt          *time.Time                   `json:"expires_at"`
	WarningAt          *time.Time                   `json:"warning_at"`
	CleanupAfter       *time.Time                   `json:"cleanup_after"`
}

type RealtimeConsumerGroupSettlementResult struct {
	Status      string     `json:"status"`
	LeaseUntil  *time.Time `json:"lease_until,omitempty"`
	AvailableAt *time.Time `json:"available_at,omitempty"`
}

type RealtimeConsumerGroupSettlement struct {
	IdempotencyKey    *string `json:"idempotency_key,omitempty"`
	Sequence          string  `json:"sequence"`
	ClaimToken        string  `json:"claim_token"`
	Action            string  `json:"action"`
	RetryAfterSeconds *int    `json:"retry_after_seconds,omitempty"`
	FailureCode       *string `json:"failure_code,omitempty"`
}

type RealtimeConsumerGroupClaim struct {
	Message          RealtimeConsumerGroupMessage `json:"message"`
	Worker           string                       `json:"worker"`
	Attempt          int                          `json:"attempt"`
	ClaimToken       string                       `json:"claim_token"`
	LeaseUntil       time.Time                    `json:"lease_until"`
	GroupID          string                       `json:"group_id"`
	FencingToken     string                       `json:"fencing_token"`
	DeliveryDeadline *time.Time                   `json:"delivery_deadline"`
}

type RealtimeWorkerClaimRequest struct {
	Worker      *string `json:"worker,omitempty"`
	WaitSeconds *int    `json:"wait_seconds,omitempty"`
}

type RealtimeConsumerGroupLagAlertConfig struct {
	Enabled              bool    `json:"enabled"`
	UnassignedThreshold  *string `json:"unassigned_threshold,omitempty"`
	OldestPendingSeconds *int    `json:"oldest_pending_seconds,omitempty"`
	DeadLetterThreshold  *int    `json:"dead_letter_threshold,omitempty"`
	CooldownSeconds      *int    `json:"cooldown_seconds,omitempty"`
}

type RealtimeConsumerGroupLagAlert struct {
	Enabled                 bool       `json:"enabled"`
	UnassignedThreshold     string     `json:"unassigned_threshold"`
	OldestPendingSeconds    int        `json:"oldest_pending_seconds"`
	DeadLetterThreshold     int        `json:"dead_letter_threshold"`
	CooldownSeconds         int        `json:"cooldown_seconds"`
	Breached                bool       `json:"breached"`
	Unassigned              *string    `json:"unassigned"`
	OldestPendingAgeSeconds *int       `json:"oldest_pending_age_seconds"`
	DeadLetters             *int       `json:"dead_letters"`
	ObservedAt              *time.Time `json:"observed_at"`
	LastEventAt             *time.Time `json:"last_event_at"`
}

type RealtimeConsumerGroupRetentionRequest struct {
	RetentionSeconds int `json:"retention_seconds"`
}

type RealtimeConsumerGroupRetentionPolicy struct {
	RetentionSeconds   int    `json:"retention_seconds"`
	WarningSeconds     int    `json:"warning_seconds"`
	ExpiredDeadLetters string `json:"expired_dead_letters"`
}

type RealtimeConsumerGroupRetryPolicyRequest struct {
	Enabled               bool     `json:"enabled"`
	BaseDelaySeconds      int      `json:"base_delay_seconds"`
	MaxDelaySeconds       int      `json:"max_delay_seconds"`
	Jitter                bool     `json:"jitter"`
	PermanentFailureCodes []string `json:"permanent_failure_codes,omitempty"`
}

type RealtimeConsumerGroupRetryStatus struct {
	Enabled               bool       `json:"enabled"`
	BaseDelaySeconds      int        `json:"base_delay_seconds"`
	MaxDelaySeconds       int        `json:"max_delay_seconds"`
	Jitter                bool       `json:"jitter"`
	PermanentFailureCodes []string   `json:"permanent_failure_codes"`
	NextRetryAt           *time.Time `json:"next_retry_at"`
}

type RealtimeConsumerGroupClaimRatePolicy struct {
	RatePerSecond int `json:"rate_per_second"`
	Burst         int `json:"burst"`
}

type RealtimeConsumerGroupClaimRateStatus struct {
	RatePerSecond          int    `json:"rate_per_second"`
	Burst                  int    `json:"burst"`
	ThrottledClaimRequests string `json:"throttled_claim_requests"`
	RetryAfterMilliseconds int64  `json:"retry_after_milliseconds"`
}

type RealtimeWorkerHeartbeat struct {
	ID         string    `json:"id"`
	Worker     string    `json:"worker"`
	Ready      bool      `json:"ready"`
	StatusCode string    `json:"status_code"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Lost       bool      `json:"lost"`
	Live       bool      `json:"live"`
}

type RealtimeWorkerHeartbeatRequest struct {
	Worker     *string `json:"worker,omitempty"`
	Ready      bool    `json:"ready"`
	StatusCode *string `json:"status_code,omitempty"`
}

type RealtimeWorkerHealth struct {
	Workers        []RealtimeWorkerHeartbeat `json:"workers"`
	LiveWorkers    int                       `json:"live_workers"`
	ReadyWorkers   int                       `json:"ready_workers"`
	TimeoutSeconds int                       `json:"timeout_seconds"`
}

type RealtimeConsumerGroupMetrics struct {
	BucketSeconds int                                 `json:"bucket_seconds"`
	Buckets       []RealtimeConsumerGroupMetricBucket `json:"buckets"`
}

type RealtimeConsumerGroupMetricBucket struct {
	BucketAt               time.Time `json:"bucket_at"`
	FirstClaims            string    `json:"first_claims"`
	Completed              string    `json:"completed"`
	Retries                string    `json:"retries"`
	DeadLetters            string    `json:"dead_letters"`
	QueueWaitMilliseconds  float64   `json:"queue_wait_milliseconds"`
	ProcessingMilliseconds float64   `json:"processing_milliseconds"`
}

type RealtimeConsumerGroupPriorityStatus struct {
	MetadataKey     string                                 `json:"metadata_key"`
	FairTurns       int                                    `json:"fair_turns"`
	NextClaimOldest bool                                   `json:"next_claim_oldest"`
	Backlog         []RealtimeConsumerGroupPriorityBacklog `json:"backlog"`
}

type RealtimeConsumerGroupPriorityBacklog struct {
	Priority int `json:"priority"`
	Ready    int `json:"ready"`
	Leased   int `json:"leased"`
	Dead     int `json:"dead"`
}

type RealtimeConsumerGroupCircuitPolicy struct {
	Enabled          bool `json:"enabled"`
	FailureThreshold int  `json:"failure_threshold"`
	CooldownSeconds  int  `json:"cooldown_seconds"`
}

type RealtimeConsumerGroupCircuitStatus struct {
	Enabled                bool       `json:"enabled"`
	FailureThreshold       int        `json:"failure_threshold"`
	CooldownSeconds        int        `json:"cooldown_seconds"`
	State                  string     `json:"state"`
	Reason                 string     `json:"reason"`
	ConsecutiveFailures    int        `json:"consecutive_failures"`
	NextProbeAt            *time.Time `json:"next_probe_at"`
	ProbeSequence          *string    `json:"probe_sequence"`
	Blocked                bool       `json:"blocked"`
	RetryAfterMilliseconds int64      `json:"retry_after_milliseconds"`
}

type RealtimeConsumerGroupDeliveryHistory struct {
	Events            []RealtimeConsumerGroupDeliveryHistoryEvent `json:"events"`
	HasMore           bool                                        `json:"has_more"`
	NextAfterID       string                                      `json:"next_after_id"`
	GroupPrunedEvents string                                      `json:"group_pruned_events"`
	RetentionSeconds  int                                         `json:"retention_seconds"`
}

type RealtimeConsumerGroupDeliveryHistoryEvent struct {
	ID           string    `json:"id"`
	Sequence     string    `json:"sequence"`
	Event        string    `json:"event"`
	Attempt      int       `json:"attempt"`
	FencingToken *string   `json:"fencing_token"`
	FailureCode  string    `json:"failure_code"`
	RecordedAt   time.Time `json:"recorded_at"`
}

type RealtimeConsumerGroupBatchSettlementResult struct {
	Results []RealtimeConsumerGroupBatchSettlementItem `json:"results"`
}

type RealtimeConsumerGroupBatchSettlementItem struct {
	Sequence    string     `json:"sequence"`
	Action      string     `json:"action"`
	Status      string     `json:"status"`
	AvailableAt *time.Time `json:"available_at,omitempty"`
	LeaseUntil  *time.Time `json:"lease_until,omitempty"`
}

type RealtimeConsumerGroupBatchSettlementRequest struct {
	IdempotencyKey *string                           `json:"idempotency_key,omitempty"`
	Settlements    []RealtimeConsumerGroupSettlement `json:"settlements"`
}

type RealtimeConsumerGroupDrainState struct {
	Draining     bool       `json:"draining"`
	Drained      bool       `json:"drained"`
	ActiveLeases int        `json:"active_leases"`
	PausedAt     *time.Time `json:"paused_at"`
}

type RealtimeConsumerGroupPauseState struct {
	Paused   bool       `json:"paused"`
	PausedAt *time.Time `json:"paused_at"`
}

type RealtimeWorkerBatchClaimRequest struct {
	Worker      *string `json:"worker,omitempty"`
	Limit       int     `json:"limit"`
	WaitSeconds *int    `json:"wait_seconds,omitempty"`
}

type RealtimeConsumerGroupBatch struct {
	DeduplicationBlocked   bool                         `json:"deduplication_blocked"`
	CircuitOpen            bool                         `json:"circuit_open"`
	Claims                 []RealtimeConsumerGroupClaim `json:"claims"`
	HistoryUnavailable     bool                         `json:"history_unavailable"`
	CapacityBlocked        bool                         `json:"capacity_blocked"`
	Paused                 bool                         `json:"paused"`
	OrderingBlocked        bool                         `json:"ordering_blocked"`
	RateThrottled          bool                         `json:"rate_throttled"`
	RetryAfterMilliseconds int64                        `json:"retry_after_milliseconds"`
	FilterScanLimited      bool                         `json:"filter_scan_limited"`
}

type RealtimeConsumerGroupBatchRequest struct {
	Worker      string `json:"worker"`
	Limit       int    `json:"limit"`
	WaitSeconds *int   `json:"wait_seconds,omitempty"`
}

type RealtimeConsumerGroup struct {
	DeduplicationMetadataKey   string                      `json:"deduplication_metadata_key"`
	DeduplicationWindowSeconds int                         `json:"deduplication_window_seconds"`
	DuplicateMessages          string                      `json:"duplicate_messages"`
	DeduplicationKeys          int                         `json:"deduplication_keys"`
	GroupID                    string                      `json:"group_id"`
	Name                       string                      `json:"name"`
	Channel                    string                      `json:"channel"`
	AssignedSequence           string                      `json:"assigned_sequence"`
	LatestSequence             string                      `json:"latest_sequence"`
	OldestSequence             string                      `json:"oldest_sequence"`
	Unassigned                 string                      `json:"unassigned"`
	Completed                  string                      `json:"completed"`
	Ready                      int                         `json:"ready"`
	Leased                     int                         `json:"leased"`
	ExpiredLeases              int                         `json:"expired_leases"`
	Dead                       int                         `json:"dead"`
	ActiveWorkers              int                         `json:"active_workers"`
	HistoryUnavailable         bool                        `json:"history_unavailable"`
	CapacityBlocked            bool                        `json:"capacity_blocked"`
	TriggerID                  *string                     `json:"trigger_id,omitempty"`
	LeaseSeconds               int                         `json:"lease_seconds"`
	MaxAttempts                int                         `json:"max_attempts"`
	CreatedAt                  time.Time                   `json:"created_at"`
	Paused                     bool                        `json:"paused"`
	PausedAt                   *time.Time                  `json:"paused_at"`
	Draining                   bool                        `json:"draining"`
	Drained                    bool                        `json:"drained"`
	OrderingMetadataKey        string                      `json:"ordering_metadata_key"`
	DeadLetterPolicy           string                      `json:"dead_letter_policy"`
	OrderingBlocked            int                         `json:"ordering_blocked"`
	LastFencingToken           string                      `json:"last_fencing_token"`
	DeadLetterRetentionSeconds int                         `json:"dead_letter_retention_seconds"`
	ExpiredDeadLetters         string                      `json:"expired_dead_letters"`
	DeadlineMetadataKey        string                      `json:"deadline_metadata_key"`
	ExpiredDeliveries          string                      `json:"expired_deliveries"`
	Filter                     RealtimeConsumerGroupFilter `json:"filter"`
	FilteredMessages           string                      `json:"filtered_messages"`
}

type RealtimeWorkerCredentialIssued struct {
	Credential string                   `json:"credential"`
	Info       RealtimeWorkerCredential `json:"info"`
}

type RealtimeWorkerCredentialRequest struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	TTLSeconds  *int     `json:"ttl_seconds,omitempty"`
}

type RealtimeWorkerCredential struct {
	ID            string     `json:"id"`
	GroupID       string     `json:"group_id"`
	Name          string     `json:"name"`
	Permissions   []string   `json:"permissions"`
	Generation    string     `json:"generation"`
	IssuedAt      time.Time  `json:"issued_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	LastOperation *string    `json:"last_operation"`
	UseCount      string     `json:"use_count"`
}

type RealtimePublisherPublishResponse struct {
	Sequence string `json:"sequence"`
	Durable  bool   `json:"durable"`
	Replayed bool   `json:"replayed"`
	Partial  bool   `json:"partial"`
}

type RealtimePublisherMessageRequest struct {
	TTLSeconds *int              `json:"ttl_seconds,omitempty"`
	EventType  string            `json:"event_type"`
	DataBase64 string            `json:"data_base64"`
	Binary     *bool             `json:"binary,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type RealtimeWorkerCredentialRotationRequest struct {
	TTLSeconds *int `json:"ttl_seconds,omitempty"`
}

type RealtimePublisherCredentialIssued struct {
	Credential string                      `json:"credential"`
	Info       RealtimePublisherCredential `json:"info"`
}

type RealtimePublisherCredentialRequest struct {
	MaxMessageTTLSeconds *int     `json:"max_message_ttl_seconds,omitempty"`
	Name                 string   `json:"name"`
	Channels             []string `json:"channels"`
	EventTypes           []string `json:"event_types"`
	PublishesPerMinute   *int     `json:"publishes_per_minute,omitempty"`
	MaxPayloadBytes      *int     `json:"max_payload_bytes,omitempty"`
	TTLSeconds           *int     `json:"ttl_seconds,omitempty"`
}

type RealtimePublisherCredential struct {
	MaxMessageTTLSeconds *int       `json:"max_message_ttl_seconds,omitempty"`
	Name                 string     `json:"name"`
	Channels             []string   `json:"channels"`
	EventTypes           []string   `json:"event_types"`
	PublishesPerMinute   int        `json:"publishes_per_minute"`
	MaxPayloadBytes      int        `json:"max_payload_bytes"`
	ID                   string     `json:"id"`
	EndpointID           string     `json:"endpoint_id"`
	Generation           string     `json:"generation"`
	IssuedAt             time.Time  `json:"issued_at"`
	ExpiresAt            time.Time  `json:"expires_at"`
	RevokedAt            *time.Time `json:"revoked_at"`
	AcceptedCount        string     `json:"accepted_count"`
	ReplayCount          string     `json:"replay_count"`
	LastUsedAt           *time.Time `json:"last_used_at"`
	LastChannel          *string    `json:"last_channel"`
	LastSequence         *string    `json:"last_sequence"`
}
