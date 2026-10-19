package internalmail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func suppressionStateRow(r q.InternalMailSuppressionState) SuppressionStatus {
	return SuppressionStatus{Address: r.Address, Blocked: r.Blocked, Revision: r.Revision, LastBlockRevision: r.LastBlockRevision,
		Reason: r.Reason, Cause: r.Cause, UpdatedAt: timePointer(r.UpdatedAt),
		Evidence: &SuppressionEvidence{SourceKind: r.SourceKind, SourceID: r.SourceID, RecipientID: idString(r.RecipientID), EventRef: r.EventRef, RequestID: idString(r.RequestID)}}
}

func (s *PGStore) suppressionStatus(ctx context.Context, address string) (SuppressionStatus, error) {
	r, err := s.queries.SuppressionState(ctx, s.pool, q.SuppressionStateParams{Namespace: s.namespace, Address: address})
	if err == nil {
		return suppressionStateRow(r), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SuppressionStatus{}, err
	}
	legacy, err := s.queries.SuppressionProjection(ctx, s.pool, q.SuppressionProjectionParams{Namespace: s.namespace, Address: address})
	if errors.Is(err, pgx.ErrNoRows) {
		return SuppressionStatus{Address: address}, nil
	}
	if err != nil {
		return SuppressionStatus{}, err
	}
	return SuppressionStatus{Address: address, Blocked: true, Revision: 1, LastBlockRevision: 1, Reason: legacy.Reason, Cause: "legacy", Evidence: &SuppressionEvidence{SourceKind: "legacy"}, UpdatedAt: timePointer(legacy.CreatedAt)}, nil
}

func (s *PGStore) suppressionHistory(ctx context.Context, address string, list OperatorList) (OperatorPage[SuppressionEvent], error) {
	after, err := suppressionHistoryList(list)
	if err != nil {
		return OperatorPage[SuppressionEvent]{}, err
	}
	rows, err := s.queries.SuppressionHistory(ctx, s.pool, q.SuppressionHistoryParams{Namespace: s.namespace, Address: address, AfterRevision: after, PageLimit: int32(list.Limit + 1)})
	if err != nil {
		return OperatorPage[SuppressionEvent]{}, err
	}
	items := make([]SuppressionEvent, 0, len(rows))
	for _, row := range rows {
		items = append(items, SuppressionEvent{Revision: row.Revision, Blocked: row.Blocked, Reason: row.Reason, Cause: row.Cause, CreatedAt: row.CreatedAt.Time,
			Evidence: SuppressionEvidence{SourceKind: row.SourceKind, SourceID: row.SourceID, RecipientID: idString(row.RecipientID), EventRef: row.EventRef, RequestID: idString(row.RequestID)}})
	}
	return operatorPage(items, list.Limit, func(e SuppressionEvent) string { return strconv.FormatInt(e.Revision, 10) }), nil
}

func (s *PGStore) ensureSuppressionState(ctx context.Context, tx pgx.Tx, address string) error {
	if err := s.queries.EnsureSuppressionState(ctx, tx, q.EnsureSuppressionStateParams{Namespace: s.namespace, Address: address}); err != nil {
		return err
	}
	return s.queries.EnsureSuppressionLegacyEvent(ctx, tx, q.EnsureSuppressionLegacyEventParams{Namespace: s.namespace, Address: address})
}

func (s *PGStore) changeSuppression(ctx context.Context, action string, input SuppressionRequest, enabled func() bool) (OperationRecord, error) {
	if input.Validate(action) != nil {
		return OperationRecord{}, ErrInvalid
	}
	return s.operatorMutation(ctx, input.OperatorAuditInput, "suppression_"+action, input.Address, input, enabled, func(tx pgx.Tx) (any, error) {
		if err := s.lockSuppressionAddress(ctx, tx, input.Address); err != nil {
			return nil, err
		}
		if err := s.ensureSuppressionState(ctx, tx, input.Address); err != nil {
			return nil, err
		}
		previous, err := s.queries.SuppressionState(ctx, tx, q.SuppressionStateParams{Namespace: s.namespace, Address: input.Address})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if previous.Revision != *input.ExpectedRevision {
			return nil, ErrSuppressionRevision
		}
		if previous.Reason == "complained" {
			return nil, ErrSuppressionProtected
		}
		// Defend complaint protection even if a legacy projection was restored
		// independently. Current binaries write both representations together.
		projection, err := s.queries.SuppressionProjection(ctx, tx, q.SuppressionProjectionParams{Namespace: s.namespace, Address: input.Address})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if projection.Reason == "complained" {
			return nil, ErrSuppressionProtected
		}
		var row q.InternalMailSuppressionState
		if action == "release" {
			if !previous.Blocked {
				return nil, ErrConflict
			}
			row, err = s.queries.ReleaseSuppressionState(ctx, tx, q.ReleaseSuppressionStateParams{Namespace: s.namespace, Address: input.Address, ExpectedRevision: *input.ExpectedRevision, RequestID: pgID(input.RequestID)})
		} else {
			row, err = s.queries.BlockSuppressionState(ctx, tx, q.BlockSuppressionStateParams{Namespace: s.namespace, Address: input.Address, Reason: "manual", Cause: "manual_add", SourceKind: "operator", RequestID: pgID(input.RequestID)})
		}
		if err != nil {
			return nil, storeError(err)
		}
		if err := s.saveSuppressionTransition(ctx, tx, row); err != nil {
			return nil, err
		}
		return suppressionStateRow(row), nil
	})
}

func (s *PGStore) saveSuppressionTransition(ctx context.Context, tx pgx.Tx, row q.InternalMailSuppressionState) error {
	if row.Blocked {
		if err := s.queries.Suppress(ctx, tx, q.SuppressParams{Namespace: s.namespace, Column2: row.Address, Reason: row.Reason}); err != nil {
			return err
		}
	} else if err := s.queries.DeleteSuppressionProjection(ctx, tx, q.DeleteSuppressionProjectionParams{Namespace: s.namespace, Address: row.Address}); err != nil {
		return err
	}
	return s.queries.RecordSuppressionEvent(ctx, tx, q.RecordSuppressionEventParams{Namespace: s.namespace, Address: row.Address})
}

func (s *PGStore) recordSuppressionReport(ctx context.Context, tx pgx.Tx, address string, report Report) error {
	if err := s.ensureSuppressionState(ctx, tx, address); err != nil {
		return err
	}
	source := report.suppressionSource
	if source.kind == "" {
		source.kind = "service_report"
	}
	if source.kind != "service_report" && source.kind != "postfix" && source.kind != "provider" ||
		source.kind == "provider" && !validFeedbackSourceID(source.id) || source.kind != "provider" && source.id != "" {
		return ErrInvalid
	}
	cause := "hard_bounce"
	if report.Kind == "complained" {
		cause = "complaint"
	}
	// Raw report identities and diagnostics can contain provider/customer data.
	// Store only a namespace/source-bound digest for evidence correlation.
	ref := sha256.Sum256([]byte("gregale-mail-suppression-event:v1\x00" + s.namespace + "\x00" + source.kind + "\x00" + source.id + "\x00" + report.EventID))
	row, err := s.queries.BlockSuppressionState(ctx, tx, q.BlockSuppressionStateParams{Namespace: s.namespace, Address: address, Reason: report.Kind, Cause: cause,
		SourceKind: source.kind, SourceID: source.id, RecipientID: pgID(report.RecipientID), EventRef: hex.EncodeToString(ref[:])})
	if err != nil {
		return err
	}
	return s.saveSuppressionTransition(ctx, tx, row)
}
