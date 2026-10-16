package state

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	exportsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

var ErrRealtimeDeadLetterExportChanged = errors.New("state: realtime dead-letter export identity or bounds changed")

type RealtimeDeadLetterExportRequest struct {
	GroupID         string
	AfterSequence   string
	ThroughSequence string
	SnapshotAt      *time.Time
	FailureCodes    []string
	Limit           int
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
type RealtimeDeadLetterExportPage struct {
	Manifest RealtimeDeadLetterExportManifest
	Records  []RealtimeDeadLetterExportRecord
}
type RealtimeConsumerGroupDeadLetterExportStore interface {
	ExportRealtimeConsumerGroupDeadLetters(context.Context, string, string, string, RealtimeDeadLetterExportRequest) (RealtimeDeadLetterExportPage, error)
}

func validateRealtimeDeadLetterExport(req *RealtimeDeadLetterExportRequest) (int64, int64, error) {
	if req.Limit == 0 {
		req.Limit = api.RealtimeConsumerGroupDeadLetterExportDefaultLimit
	}
	if req.Limit < 1 || req.Limit > api.RealtimeConsumerGroupDeadLetterExportMaxRows {
		return 0, 0, ErrRealtimeConsumerGroupInvalid
	}
	if req.AfterSequence == "" {
		req.AfterSequence = "0"
	}
	after, err := realtimeGroupSequence(req.AfterSequence)
	if err != nil {
		return 0, 0, err
	}
	through := int64(0)
	resume := req.GroupID != "" || req.ThroughSequence != "" || req.SnapshotAt != nil
	if resume {
		id, err := uuid.Parse(req.GroupID)
		if err != nil || len(req.GroupID) != 36 || req.ThroughSequence == "" || req.SnapshotAt == nil || req.SnapshotAt.Year() < 1 || req.SnapshotAt.Year() > 9999 {
			return 0, 0, ErrRealtimeConsumerGroupInvalid
		}
		req.GroupID = id.String()
		through, err = realtimeGroupSequence(req.ThroughSequence)
		if err != nil || through < after {
			return 0, 0, ErrRealtimeConsumerGroupInvalid
		}
	}
	if len(req.FailureCodes) > api.RealtimeConsumerGroupPermanentFailureCodesMax {
		return 0, 0, ErrRealtimeConsumerGroupInvalid
	}
	err = validateRealtimeRetryPolicy(RealtimeConsumerGroupRetryPolicy{BaseDelaySeconds: 1, MaxDelaySeconds: 1, PermanentFailureCodes: req.FailureCodes})
	if req.FailureCodes == nil {
		req.FailureCodes = []string{}
	}
	return after, through, err
}

func (s *PgStore) ExportRealtimeConsumerGroupDeadLetters(ctx context.Context, ep, ch, name string, req RealtimeDeadLetterExportRequest) (RealtimeDeadLetterExportPage, error) {
	page := RealtimeDeadLetterExportPage{Records: []RealtimeDeadLetterExportRecord{}}
	if _, ok := realtimeWorkerFromContext(ctx); ok {
		return page, ErrRealtimeWorkerCredentialDenied
	}
	if err := validateRealtimeConsumerGroup(ep, ch, name); err != nil {
		return page, err
	}
	after, through, err := validateRealtimeDeadLetterExport(&req)
	if err != nil {
		return page, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := exportsqlc.New()
	group, err := queries.ReadDeadLetterExportGroup(ctx, tx, exportsqlc.ReadDeadLetterExportGroupParams{EndpointID: ep, Channel: ch, GroupName: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil {
		return page, err
	}
	snapshot := group.ObservedAt.Time.UTC()
	if req.GroupID == "" {
		through = group.AssignedSequence
	} else {
		if req.GroupID != group.GroupID || through > group.AssignedSequence {
			return page, ErrRealtimeDeadLetterExportChanged
		}
		snapshot = req.SnapshotAt.UTC()
		if snapshot.After(group.ObservedAt.Time) {
			return page, ErrRealtimeConsumerGroupInvalid
		}
	}
	if after > through {
		return page, ErrRealtimeConsumerGroupInvalid
	}
	rows, err := queries.ReadDeadLetterExportRows(ctx, tx, exportsqlc.ReadDeadLetterExportRowsParams{GroupID: group.GroupID, AfterSequence: after, ThroughSequence: through, SnapshotAt: pgtype.Timestamptz{Time: snapshot, Valid: true}, FailureCodes: req.FailureCodes, RowLimit: int32(req.Limit + 1), RetentionSeconds: group.DeadLetterRetentionSeconds})
	if err != nil {
		return page, err
	}
	page.Manifest = RealtimeDeadLetterExportManifest{Type: "manifest", FormatVersion: api.RealtimeConsumerGroupDeadLetterExportFormatVersion, GroupID: group.GroupID, Group: name, Channel: ch, ExportedAt: group.ObservedAt.Time.UTC(), SnapshotAt: snapshot, AfterSequence: strconv.FormatInt(after, 10), ThroughSequence: strconv.FormatInt(through, 10), FailureCodes: req.FailureCodes, HasMore: len(rows) > req.Limit, NextAfterSequence: strconv.FormatInt(after, 10)}
	if page.Manifest.HasMore {
		rows = rows[:req.Limit]
	}
	for _, row := range rows {
		record, err := realtimeDeadLetterExportRecord(group.GroupID, row)
		if err != nil {
			return page, err
		}
		page.Records = append(page.Records, record)
		page.Manifest.NextAfterSequence = row.Sequence
	}
	page.Manifest.Count = len(page.Records)
	return page, tx.Commit(ctx)
}

func realtimeDeadLetterExportRecord(group string, row exportsqlc.ReadDeadLetterExportRowsRow) (RealtimeDeadLetterExportRecord, error) {
	record := RealtimeDeadLetterExportRecord{Type: "dead_letter", GroupID: group, Sequence: row.Sequence, DeadSince: row.DeadSince.Time.UTC(), Attempts: int(row.Attempts), FailureCode: row.FailureCode, DeliveryDeadline: timestamptzToTimePtr(row.DeliveryDeadline), DeadlineInvalid: row.DeadlineInvalid, OrderingKey: row.OrderingKey, Priority: int(row.Priority), ExpiresAt: timestamptzToTimePtr(row.ExpiresAt)}
	if !row.DeadSince.Valid {
		return record, errors.New("state: dead-letter export missing failure timestamp")
	}
	if row.FencingToken != "" {
		fence := row.FencingToken
		record.FencingToken = &fence
	}
	err := json.Unmarshal(row.Message, &record.Message)
	record.Message.CreatedAt = record.Message.CreatedAt.UTC()
	if record.DeliveryDeadline != nil {
		value := record.DeliveryDeadline.UTC()
		record.DeliveryDeadline = &value
	}
	if record.ExpiresAt != nil {
		value := record.ExpiresAt.UTC()
		record.ExpiresAt = &value
	}
	return record, err
}
