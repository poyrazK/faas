package state

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrRealtimeConsumerGroupInvalid         = errors.New("state: invalid realtime consumer group request")
	ErrRealtimeConsumerGroupLease           = errors.New("state: realtime consumer group lease expired or replaced")
	ErrRealtimeConsumerGroupGap             = errors.New("state: realtime consumer group cursor outside retained history")
	ErrRealtimeConsumerGroupOrderingBlocked = errors.New("state: realtime consumer group ordering blocked")
	ErrRealtimeConsumerGroupLimit           = errors.New("state: realtime consumer group capacity reached")
)

type RealtimeConsumerGroupCapacityError struct {
	Resource        string
	Limit, Observed int
}

func (e *RealtimeConsumerGroupCapacityError) Error() string {
	return "state: realtime consumer group capacity reached"
}
func (e *RealtimeConsumerGroupCapacityError) Unwrap() error { return ErrRealtimeConsumerGroupLimit }

type RealtimeConsumerGroupConfig struct {
	DeduplicationMetadataKey   string                       `json:"deduplication_metadata_key,omitempty"`
	DeduplicationWindowSeconds int                          `json:"deduplication_window_seconds,omitempty"`
	PriorityMetadataKey        string                       `json:"priority_metadata_key,omitempty"`
	Filter                     *RealtimeConsumerGroupFilter `json:"filter,omitempty"`
	DeadlineMetadataKey        string                       `json:"deadline_metadata_key,omitempty"`
	DeadLetterRetentionSeconds int                          `json:"dead_letter_retention_seconds,omitempty"`
	OrderingMetadataKey        string                       `json:"ordering_metadata_key,omitempty"`
	DeadLetterPolicy           string                       `json:"dead_letter_policy,omitempty"`
	AfterSequence              string                       `json:"after_sequence"`
	LeaseSeconds               int                          `json:"lease_seconds,omitempty"`
	MaxAttempts                int                          `json:"max_attempts,omitempty"`
}
type RealtimeConsumerGroup struct {
	DeduplicationMetadataKey   string                      `json:"deduplication_metadata_key"`
	DeduplicationWindowSeconds int                         `json:"deduplication_window_seconds"`
	DuplicateMessages          string                      `json:"duplicate_messages"`
	DeduplicationKeys          int                         `json:"deduplication_keys"`
	Filter                     RealtimeConsumerGroupFilter `json:"filter"`
	FilteredMessages           string                      `json:"filtered_messages"`
	DeadlineMetadataKey        string                      `json:"deadline_metadata_key"`
	ExpiredDeliveries          string                      `json:"expired_deliveries"`
	DeadLetterRetentionSeconds int                         `json:"dead_letter_retention_seconds"`
	ExpiredDeadLetters         string                      `json:"expired_dead_letters"`
	LastFencingToken           string                      `json:"last_fencing_token"`
	OrderingMetadataKey        string                      `json:"ordering_metadata_key"`
	DeadLetterPolicy           string                      `json:"dead_letter_policy"`
	OrderingBlocked            int                         `json:"ordering_blocked"`
	Draining                   bool                        `json:"draining"`
	Drained                    bool                        `json:"drained"`
	Paused                     bool                        `json:"paused"`
	PausedAt                   *time.Time                  `json:"paused_at"`
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
	// TriggerID names the realtime trigger consuming this group (ADR-1322).
	TriggerID    string    `json:"trigger_id,omitempty"`
	LeaseSeconds int       `json:"lease_seconds"`
	MaxAttempts  int       `json:"max_attempts"`
	CreatedAt    time.Time `json:"created_at"`
}
type RealtimeConsumerGroupMessage struct {
	Sequence        string            `json:"sequence"`
	DataBase64      string            `json:"data_base64"`
	Binary          bool              `json:"binary"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	TargetMessageID string            `json:"target_message_id,omitempty"`
	Version         int64             `json:"version"`
	Event           string            `json:"event"`
	Deleted         bool              `json:"deleted"`
	CreatedAt       time.Time         `json:"created_at"`
}
type RealtimeConsumerGroupClaim struct {
	DeliveryDeadline *time.Time                   `json:"delivery_deadline"`
	GroupID          string                       `json:"group_id"`
	FencingToken     string                       `json:"fencing_token"`
	Message          RealtimeConsumerGroupMessage `json:"message"`
	Worker           string                       `json:"worker"`
	Attempt          int                          `json:"attempt"`
	ClaimToken       string                       `json:"claim_token"`
	LeaseUntil       time.Time                    `json:"lease_until"`
}
type RealtimeConsumerGroupDeadLetter struct {
	RetentionStartedAt *time.Time                   `json:"retention_started_at"`
	ExpiresAt          *time.Time                   `json:"expires_at"`
	WarningAt          *time.Time                   `json:"warning_at"`
	CleanupAfter       *time.Time                   `json:"cleanup_after"`
	Message            RealtimeConsumerGroupMessage `json:"message"`
	Attempts           int                          `json:"attempts"`
	FailureCode        string                       `json:"failure_code"`
}
type RealtimeConsumerGroupSettlement struct {
	IdempotencyKey    string `json:"idempotency_key,omitempty"`
	Sequence          string `json:"sequence"`
	ClaimToken        string `json:"claim_token"`
	Action            string `json:"action"` // complete, retry, renew, release
	RetryAfterSeconds *int   `json:"retry_after_seconds,omitempty"`
	FailureCode       string `json:"failure_code,omitempty"`
}
type RealtimeConsumerGroupSettlementResult struct {
	AvailableAt *time.Time `json:"available_at,omitempty"`
	Status      string     `json:"status"`
	LeaseUntil  *time.Time `json:"lease_until,omitempty"`
}
type RealtimeConsumerGroupStore interface {
	CreateRealtimeConsumerGroup(context.Context, string, string, string, RealtimeConsumerGroupConfig) (RealtimeConsumerGroup, error)
	ListRealtimeConsumerGroups(context.Context, string, string) ([]RealtimeConsumerGroup, error)
	DeleteRealtimeConsumerGroup(context.Context, string, string, string) error
	ClaimRealtimeConsumerGroup(context.Context, string, string, string, string) (*RealtimeConsumerGroupClaim, error)
	SettleRealtimeConsumerGroup(context.Context, string, string, string, RealtimeConsumerGroupSettlement) (RealtimeConsumerGroupSettlementResult, error)
	ListRealtimeConsumerGroupDeadLetters(context.Context, string, string, string) ([]RealtimeConsumerGroupDeadLetter, error)
	ReplayRealtimeConsumerGroupDeadLetter(context.Context, string, string, string, string) error
	ResetRealtimeConsumerGroup(context.Context, string, string, string, string, string) error
}

func validateRealtimeConsumerGroup(ep, ch, name string) error {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || !validRealtimeWorkerName(name, api.RealtimeConsumerGroupNameMaxBytes) {
		return ErrRealtimeConsumerGroupInvalid
	}
	return nil
}
func validRealtimeWorkerName(name string, max int) bool {
	return len(name) > 0 && len(name) <= max && strings.TrimSpace(name) == name && !strings.ContainsAny(name, "\x00\r\n")
}
func realtimeGroupSequence(value string) (int64, error) {
	if value == "" {
		return 0, ErrRealtimeConsumerGroupInvalid
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, ErrRealtimeConsumerGroupInvalid
		}
	}
	seq, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, ErrRealtimeConsumerGroupInvalid
	}
	return seq, nil
}
func realtimeGroupMessage(msg ManagedRealtimeChannelMessage) RealtimeConsumerGroupMessage {
	return RealtimeConsumerGroupMessage{Sequence: strconv.FormatInt(msg.Sequence, 10), DataBase64: base64.StdEncoding.EncodeToString(msg.Data), Binary: msg.Binary, Metadata: msg.Metadata, TargetMessageID: msg.TargetMessageID, Version: msg.Version, Event: msg.MessageEvent, Deleted: msg.Deleted, CreatedAt: msg.CreatedAt}
}

// Batch claims commit together, with independent lease tokens and settlement.
type RealtimeConsumerGroupBatchRequest struct {
	WaitSeconds int    `json:"wait_seconds,omitempty"`
	Worker      string `json:"worker,omitempty"`
	Limit       int    `json:"limit"`
}
type RealtimeConsumerGroupBatch struct {
	DeduplicationBlocked   bool                         `json:"deduplication_blocked"`
	CircuitOpen            bool                         `json:"circuit_open"`
	FilterScanLimited      bool                         `json:"filter_scan_limited"`
	RateThrottled          bool                         `json:"rate_throttled"`
	RetryAfterMilliseconds int64                        `json:"retry_after_milliseconds"`
	OrderingBlocked        bool                         `json:"ordering_blocked"`
	Paused                 bool                         `json:"paused"`
	Claims                 []RealtimeConsumerGroupClaim `json:"claims"`
	HistoryUnavailable     bool                         `json:"history_unavailable"`
	CapacityBlocked        bool                         `json:"capacity_blocked"`
	outstanding            int
}
type RealtimeConsumerGroupBatchStore interface {
	ClaimRealtimeConsumerGroupBatch(context.Context, string, string, string, string, int) (RealtimeConsumerGroupBatch, error)
}

type RealtimeConsumerGroupPauseState struct {
	Paused   bool       `json:"paused"`
	PausedAt *time.Time `json:"paused_at"`
}
type RealtimeConsumerGroupPauseStore interface {
	SetRealtimeConsumerGroupPaused(context.Context, string, string, string, bool) (RealtimeConsumerGroupPauseState, error)
}

type RealtimeConsumerGroupDrainState struct {
	Draining     bool       `json:"draining"`
	Drained      bool       `json:"drained"`
	ActiveLeases int        `json:"active_leases"`
	PausedAt     *time.Time `json:"paused_at"`
}
type RealtimeConsumerGroupDrainStore interface {
	DrainRealtimeConsumerGroup(context.Context, string, string, string) (RealtimeConsumerGroupDrainState, error)
}

func validateRealtimeGroupOrdering(cfg *RealtimeConsumerGroupConfig) error {
	if cfg.DeadLetterPolicy == "" {
		cfg.DeadLetterPolicy = "block"
	}
	if cfg.DeadLetterPolicy != "block" && cfg.DeadLetterPolicy != "skip" {
		return ErrRealtimeConsumerGroupInvalid
	}
	if cfg.OrderingMetadataKey != "" && (len(cfg.OrderingMetadataKey) > api.RealtimeConsumerGroupOrderingFieldMaxBytes || api.ValidateRealtimeMetadata(map[string]string{cfg.OrderingMetadataKey: ""}) != nil) {
		return ErrRealtimeConsumerGroupInvalid
	}

	return nil
}
