package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type lineageRepairProgress struct {
	CaseBackfill *api.EventExecutionLineageCaseBackfill `json:"case_backfill,omitempty"`
	Cursor       int64                                  `json:"cursor"`
	Scanned      int64                                  `json:"scanned"`
	Repaired     int64                                  `json:"repaired"`
	Unresolved   int64                                  `json:"unresolved"`
	Conflicts    int64                                  `json:"conflicts"`
	LastScan     *time.Time                             `json:"last_scan,omitempty"`
}

func decodeLineageRepairProgress(raw []byte) (lineageRepairProgress, error) {
	var p lineageRepairProgress
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return p, err
		}
	}
	if p.Cursor < 0 || p.Scanned < 0 || p.Repaired < 0 || p.Unresolved < 0 || p.Conflicts < 0 {
		return p, ErrInvalidArgument
	}
	return p, nil
}
func applyLineageRepairProgress(out *api.EventExecutionReconciliation, p lineageRepairProgress) {
	out.RepairScanned = p.Scanned
	out.RepairRepaired = p.Repaired
	out.RepairUnresolved = p.Unresolved
	out.RepairConflicts = p.Conflicts
}
func sameLineageEventContent(a, b []byte) bool {
	decode := func(raw []byte) (any, error) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v any
		err := d.Decode(&v)
		if err != nil {
			return nil, err
		}
		var extra any
		if err = d.Decode(&extra); err != io.EOF {
			return nil, ErrInvalidArgument
		}
		return v, nil
	}
	x, err := decode(a)
	if err != nil {
		return false
	}
	y, err := decode(b)
	return err == nil && reflect.DeepEqual(x, y)
}

func (s *PgStore) repairEventExecutionLineage(ctx context.Context, limit int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	jobs, err := q.EventExecutionLineageRepairJobs(ctx, tx, int32(limit))
	if err != nil {
		return err
	}
	remaining := api.EventExecutionLineageRepairBatch
	for _, job := range jobs {
		if remaining == 0 {
			break
		}
		p, err := decodeLineageRepairProgress(job.LineageRepairProgress)
		if err != nil {
			return err
		}
		rows, err := q.EventExecutionLineageRepairReceipts(ctx, tx, sqlc.EventExecutionLineageRepairReceiptsParams{AccountID: job.AccountID, AppID: job.AppID, SubscriptionID: uuidString(job.SubscriptionID), AfterOutboxID: p.Cursor, RootLimit: int32(remaining)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			outcome, err := repairEventExecutionLineageRow(ctx, q, tx, job, row)
			if err != nil {
				return err
			}
			if err = recordLineageCaseTx(ctx, q, tx, job, row, outcome); err != nil {
				return err
			}
			p.Scanned++
			switch lineageOutcomeStatus(outcome) {
			case "repaired":
				p.Repaired++
			case "conflict":
				p.Conflicts++
			case "unresolved":
				p.Unresolved++
			}
			if row.OutboxID > p.Cursor {
				p.Cursor = row.OutboxID
			}
			remaining--
		}
		now := time.Now().UTC()
		p.LastScan = &now
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if err = q.SaveEventExecutionLineageRepairProgress(ctx, tx, sqlc.SaveEventExecutionLineageRepairProgressParams{SubscriptionID: job.SubscriptionID, AccountID: job.AccountID, AppID: job.AppID, Progress: raw}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func repairEventExecutionLineageRow(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventExecutionLineageRepairJobsRow, row sqlc.EventExecutionLineageRepairReceiptsRow, expected ...*time.Time) (string, error) {
	return evaluateEventExecutionLineageRow(ctx, q, tx, job, row, true, expected...)
}
func evaluateEventExecutionLineageRow(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventExecutionLineageRepairJobsRow, row sqlc.EventExecutionLineageRepairReceiptsRow, associate bool, expected ...*time.Time) (string, error) {

	var envelope eventcontract.Envelope
	validEnvelope := json.Unmarshal(row.Payload, &envelope) == nil
	if !validEnvelope || envelope.ID != row.EventID || envelope.Source != row.Source || envelope.AccountID != row.InvocationAccountID {
		return "unresolved:invalid_receipt", nil
	}
	id := PublishedEventInvocationID(envelope.AccountID, envelope.Source, envelope.ID, uuidString(job.SubscriptionID))
	if !associate {
		known, err := q.EventExecutionLineageBackfillAssociated(ctx, tx, sqlc.EventExecutionLineageBackfillAssociatedParams{InvocationID: mustPgUUID(id), AccountID: job.AccountID, AppID: job.AppID, SubscriptionID: job.SubscriptionID, OutboxID: row.OutboxID, AcceptedAt: row.AcceptedAt})
		if err != nil {
			return "", err
		}
		if known {
			return "already_associated", nil
		}
	}
	inv, err := q.EventExecutionLineageRepairRoot(ctx, tx, sqlc.EventExecutionLineageRepairRootParams{InvocationID: mustPgUUID(id), AccountID: job.AccountID, AppID: job.AppID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "unresolved:missing_root", nil
	}
	if err != nil {
		return "", err
	}
	if inv.ReplayRootInvocationID.Valid || inv.ReplayRootCreatedAt.Valid || timeFromPgtype(inv.CreatedAt).Before(timeFromPgtype(row.AcceptedAt)) || inv.Source != string(InvocationAsyncInvoke) || inv.Method != "POST" || inv.Path != "/" {
		return "unresolved:incompatible_execution", nil
	}
	if len(expected) > 0 && expected[0] != nil && !timeFromPgtype(inv.CreatedAt).Equal(*expected[0]) {
		return "unresolved:root_incarnation_changed", nil
	}
	if !sameLineageEventContent(row.Payload, inv.Payload) {
		return "unresolved:event_content_mismatch", nil
	}
	prior, err := q.EventExecutionLineageRepairExistingRoot(ctx, tx, sqlc.EventExecutionLineageRepairExistingRootParams{InvocationID: inv.ID, CreatedAt: inv.CreatedAt})
	if err == nil {
		if uuidString(prior.SubscriptionID) != uuidString(job.SubscriptionID) || uuidString(prior.AccountID) != uuidString(job.AccountID) || uuidString(prior.AppID) != uuidString(job.AppID) || !timeFromPgtype(prior.AcceptedAt).Equal(timeFromPgtype(row.AcceptedAt)) || prior.OutboxID != row.OutboxID {
			return "conflict:conflicting_lineage", nil
		}
		return "already_associated", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	ambiguous, err := q.EventExecutionLineageRepairAmbiguous(ctx, tx, sqlc.EventExecutionLineageRepairAmbiguousParams{AccountID: job.AccountID, AppID: job.AppID, SubscriptionID: uuidString(job.SubscriptionID), EventSource: row.Source, EventID: row.EventID, InvocationAccountID: row.InvocationAccountID, OutboxID: row.OutboxID, CreatedAt: inv.CreatedAt})
	if err != nil {
		return "", err
	}
	if ambiguous {
		return "conflict:competing_receipts", nil
	}
	if !associate {
		return "unresolved:lineage_association_missing", nil
	}
	associated, err := q.AssociateEventExecutionRoot(ctx, tx, sqlc.AssociateEventExecutionRootParams{InvocationID: inv.ID, SubscriptionID: job.SubscriptionID, AccountID: job.AccountID, AppID: job.AppID, AcceptedAt: row.AcceptedAt, OutboxID: row.OutboxID, RetentionSeconds: int32(api.EventExecutionLedgerRetention / time.Second)})
	if err != nil {
		return "", err
	}
	if !associated.Associated {
		return "conflict:association_changed", nil
	}
	return "repaired", nil
}

func lineageRepairRecipient(receipt *PublishedEventWork, app, sub string) (PublishedEventRecipient, bool) {
	var recipient PublishedEventRecipient
	captured := false
	for _, r := range receipt.RecipientSnapshot {
		if sameMemUUID(r.ID, sub) && sameMemUUID(r.AppID, app) {
			recipient = r
			captured = true
			break
		}
	}
	progress := receipt.RecipientProgress[recipient.ID].State
	for _, r := range receipt.routingRecipients {
		if !sameMemUUID(r.Recipient.ID, sub) || !sameMemUUID(r.Recipient.AppID, app) {
			continue
		}
		if !captured && r.BackfillJobID == "" {
			continue
		}
		recipient = r.Recipient
		progress = r.State
		captured = true
		break
	}
	return recipient, captured && len(recipient.Workflow) == 0 && recipient.ObjectNotification == nil && progress == PublishedEventRecipientEnqueued
}
func (m *MemStore) repairEventExecutionLineageLocked(account, app, sub string, budget int) int {
	c := m.eventExecutionCoverage[sub]
	p := c.repair
	receipts := []*PublishedEventWork{}
	for _, r := range m.eventFanout {
		if r.ID <= p.Cursor || !sameMemUUID(eventRoutingAccount(r), account) {
			continue
		}
		if _, ok := lineageRepairRecipient(r, app, sub); ok {
			receipts = append(receipts, r)
		}
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].ID < receipts[j].ID })
	if len(receipts) > budget {
		receipts = receipts[:budget]
	}
	for _, r := range receipts {
		if item, exists := c.cases[r.ID]; exists && !item.AcceptedAt.Equal(r.CreatedAt) {
			break
		}
		outcome := m.repairEventExecutionLineageRowLocked(account, app, sub, r)
		m.recordLineageCaseLocked(account, app, sub, r, outcome)
		p.Scanned++
		switch lineageOutcomeStatus(outcome) {
		case "repaired":
			p.Repaired++
		case "conflict":
			p.Conflicts++
		case "unresolved":
			p.Unresolved++
		}
		if r.ID > p.Cursor {
			p.Cursor = r.ID
		}
	}
	now := time.Now().UTC()
	p.LastScan = &now
	c = m.eventExecutionCoverage[sub]
	c.repair = p
	m.eventExecutionCoverage[sub] = c
	return len(receipts)
}
func (m *MemStore) repairEventExecutionLineageRowLocked(account, app, sub string, receipt *PublishedEventWork, expected ...*time.Time) string {
	return m.evaluateEventExecutionLineageRowLocked(account, app, sub, receipt, true, expected...)
}
func (m *MemStore) evaluateEventExecutionLineageRowLocked(account, app, sub string, receipt *PublishedEventWork, associate bool, expected ...*time.Time) string {

	var envelope eventcontract.Envelope
	if json.Unmarshal(receipt.Payload, &envelope) != nil {
		return "unresolved:invalid_receipt"
	}
	recipient, ok := lineageRepairRecipient(receipt, app, sub)
	if !ok || !sameMemUUID(recipient.AccountID, account) {
		return "unresolved:incompatible_execution"
	}
	id := PublishedEventInvocationID(envelope.AccountID, envelope.Source, envelope.ID, recipient.ID)
	if !associate {
		for key, root := range m.eventExecutionRoots {
			if key.id == id && sameMemUUID(root.account, account) && sameMemUUID(root.app, app) && sameMemUUID(root.subscription, sub) && root.outbox == receipt.ID && root.accepted.Equal(receipt.CreatedAt) {
				return "already_associated"
			}
		}
	}
	inv, ok := m.invocations[id]
	if !ok {
		return "unresolved:missing_root"
	}
	if !sameMemUUID(inv.AccountID, account) || !sameMemUUID(inv.AppID, app) || inv.ReplayRootInvocationID != "" || inv.ReplayRootCreatedAt != nil || inv.CreatedAt.Before(receipt.CreatedAt) || inv.Source != InvocationAsyncInvoke || inv.Method != "POST" || inv.Path != "/" {
		return "unresolved:incompatible_execution"
	}
	if len(expected) > 0 && expected[0] != nil && !inv.CreatedAt.Equal(*expected[0]) {
		return "unresolved:root_incarnation_changed"
	}
	if !sameLineageEventContent(receipt.Payload, inv.Payload) {
		return "unresolved:event_content_mismatch"
	}
	key := ledgerRootKey(inv.ID, inv.CreatedAt)
	if prior, ok := m.eventExecutionRoots[key]; ok {
		if !sameMemUUID(prior.subscription, sub) || !sameMemUUID(prior.account, account) || !sameMemUUID(prior.app, app) || !prior.accepted.Equal(receipt.CreatedAt) || prior.outbox != receipt.ID {
			return "conflict:conflicting_lineage"
		}
		return "already_associated"
	}
	for _, other := range m.eventFanout {
		if other.ID == receipt.ID || other.CreatedAt.After(inv.CreatedAt) || !sameMemUUID(eventRoutingAccount(other), account) {
			continue
		}
		var e eventcontract.Envelope
		if json.Unmarshal(other.Payload, &e) != nil || e.AccountID != envelope.AccountID || e.Source != envelope.Source || e.ID != envelope.ID {
			continue
		}
		// Any captured competing acceptance is ambiguous, even if it never routed.
		for _, r := range other.RecipientSnapshot {
			if sameMemUUID(r.ID, sub) && sameMemUUID(r.AppID, app) {
				return "conflict:competing_receipts"
			}
		}
		for _, r := range other.routingRecipients {
			if sameMemUUID(r.Recipient.ID, sub) && sameMemUUID(r.Recipient.AppID, app) {
				return "conflict:competing_receipts"
			}
		}
	}
	if !associate {
		return "unresolved:lineage_association_missing"
	}
	if m.eventExecutionRoots == nil {
		m.eventExecutionRoots = map[executionLedgerRootKey]executionLedgerRoot{}
	}
	m.eventExecutionRoots[key] = executionLedgerRoot{key: key, account: canonicalMemUUID(account), app: canonicalMemUUID(app), subscription: canonicalMemUUID(sub), created: inv.CreatedAt, accepted: receipt.CreatedAt, outbox: receipt.ID}
	return "repaired"
}

func lineageOutcomeStatus(outcome string) string { return strings.SplitN(outcome, ":", 2)[0] }
func lineageOutcomeReason(outcome string) string {
	parts := strings.SplitN(outcome, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return outcome
}
