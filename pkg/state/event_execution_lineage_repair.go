package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type lineageRepairProgress struct {
	Cursor     int64      `json:"cursor"`
	Scanned    int64      `json:"scanned"`
	Repaired   int64      `json:"repaired"`
	Unresolved int64      `json:"unresolved"`
	Conflicts  int64      `json:"conflicts"`
	LastScan   *time.Time `json:"last_scan,omitempty"`
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
			p.Scanned++
			switch outcome {
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
func repairEventExecutionLineageRow(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventExecutionLineageRepairJobsRow, row sqlc.EventExecutionLineageRepairReceiptsRow) (string, error) {
	var envelope eventcontract.Envelope
	if json.Unmarshal(row.Payload, &envelope) != nil || envelope.ID != row.EventID || envelope.Source != row.Source || envelope.AccountID != row.InvocationAccountID {
		return "unresolved", nil
	}
	id := PublishedEventInvocationID(envelope.AccountID, envelope.Source, envelope.ID, uuidString(job.SubscriptionID))
	inv, err := q.EventExecutionLineageRepairRoot(ctx, tx, sqlc.EventExecutionLineageRepairRootParams{InvocationID: mustPgUUID(id), AccountID: job.AccountID, AppID: job.AppID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "unresolved", nil
	}
	if err != nil {
		return "", err
	}
	if inv.ReplayRootInvocationID.Valid || inv.ReplayRootCreatedAt.Valid || timeFromPgtype(inv.CreatedAt).Before(timeFromPgtype(row.AcceptedAt)) || inv.Source != string(InvocationAsyncInvoke) || inv.Method != "POST" || inv.Path != "/" || !sameLineageEventContent(row.Payload, inv.Payload) {
		return "unresolved", nil
	}
	prior, err := q.EventExecutionLineageRepairExistingRoot(ctx, tx, sqlc.EventExecutionLineageRepairExistingRootParams{InvocationID: inv.ID, CreatedAt: inv.CreatedAt})
	if err == nil {
		if uuidString(prior.SubscriptionID) != uuidString(job.SubscriptionID) || uuidString(prior.AccountID) != uuidString(job.AccountID) || uuidString(prior.AppID) != uuidString(job.AppID) || !timeFromPgtype(prior.AcceptedAt).Equal(timeFromPgtype(row.AcceptedAt)) || prior.OutboxID != row.OutboxID {
			return "conflict", nil
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
		return "conflict", nil
	}
	associated, err := q.AssociateEventExecutionRoot(ctx, tx, sqlc.AssociateEventExecutionRootParams{InvocationID: inv.ID, SubscriptionID: job.SubscriptionID, AccountID: job.AccountID, AppID: job.AppID, AcceptedAt: row.AcceptedAt, OutboxID: row.OutboxID, RetentionSeconds: int32(api.EventExecutionLedgerRetention / time.Second)})
	if err != nil {
		return "", err
	}
	if !associated.Associated {
		return "conflict", nil
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
		outcome := m.repairEventExecutionLineageRowLocked(account, app, sub, r)
		p.Scanned++
		switch outcome {
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
func (m *MemStore) repairEventExecutionLineageRowLocked(account, app, sub string, receipt *PublishedEventWork) string {
	var envelope eventcontract.Envelope
	if json.Unmarshal(receipt.Payload, &envelope) != nil {
		return "unresolved"
	}
	recipient, ok := lineageRepairRecipient(receipt, app, sub)
	if !ok || !sameMemUUID(recipient.AccountID, account) {
		return "unresolved"
	}
	id := PublishedEventInvocationID(envelope.AccountID, envelope.Source, envelope.ID, recipient.ID)
	inv, ok := m.invocations[id]
	if !ok || !sameMemUUID(inv.AccountID, account) || !sameMemUUID(inv.AppID, app) || inv.ReplayRootInvocationID != "" || inv.ReplayRootCreatedAt != nil || inv.CreatedAt.Before(receipt.CreatedAt) || inv.Source != InvocationAsyncInvoke || inv.Method != "POST" || inv.Path != "/" || !sameLineageEventContent(receipt.Payload, inv.Payload) {
		return "unresolved"
	}
	key := ledgerRootKey(inv.ID, inv.CreatedAt)
	if prior, ok := m.eventExecutionRoots[key]; ok {
		if !sameMemUUID(prior.subscription, sub) || !sameMemUUID(prior.account, account) || !sameMemUUID(prior.app, app) || !prior.accepted.Equal(receipt.CreatedAt) || prior.outbox != receipt.ID {
			return "conflict"
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
				return "conflict"
			}
		}
		for _, r := range other.routingRecipients {
			if sameMemUUID(r.Recipient.ID, sub) && sameMemUUID(r.Recipient.AppID, app) {
				return "conflict"
			}
		}
	}
	if m.eventExecutionRoots == nil {
		m.eventExecutionRoots = map[executionLedgerRootKey]executionLedgerRoot{}
	}
	m.eventExecutionRoots[key] = executionLedgerRoot{key: key, account: canonicalMemUUID(account), app: canonicalMemUUID(app), subscription: canonicalMemUUID(sub), created: inv.CreatedAt, accepted: receipt.CreatedAt, outbox: receipt.ID}
	return "repaired"
}
