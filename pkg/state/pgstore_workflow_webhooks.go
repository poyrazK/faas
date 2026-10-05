package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func webhookAutomationBindingFromSQL(row sqlc.WorkflowWebhookBinding) WebhookAutomationBinding {
	return WebhookAutomationBinding{EndpointID: pgUUIDString(row.EndpointID), WorkflowName: row.WorkflowName, EventType: row.EventType, Filter: row.Filter, Version: row.Version, UpdatedAt: row.UpdatedAt.Time}
}
func lockWebhookAutomationTarget(ctx context.Context, tx pgx.Tx, opts WebhookAutomationBindingOptions) (InboundWebhookEndpoint, sqlc.LockEventWorkflowTargetRow, error) {
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, opts.AppID); err != nil {
		return InboundWebhookEndpoint{}, sqlc.LockEventWorkflowTargetRow{}, err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(opts.AppID))
	if err != nil {
		return InboundWebhookEndpoint{}, target, mapErr(err)
	}
	if pgUUIDString(target.AccountID) != opts.AccountID || target.AppStatus == string(AppDeleted) {
		return InboundWebhookEndpoint{}, target, ErrNotFound
	}
	row, err := q.LockWebhookAutomationEndpoint(ctx, tx, sqlc.LockWebhookAutomationEndpointParams{ID: mustPgUUID(opts.EndpointID), AppID: mustPgUUID(opts.AppID), AccountID: mustPgUUID(opts.AccountID)})
	if err != nil {
		return InboundWebhookEndpoint{}, target, mapErr(err)
	}
	return InboundWebhookEndpoint{ID: opts.EndpointID, AppID: opts.AppID, AccountID: opts.AccountID, Provider: InboundWebhookProvider(row.Provider), Enabled: row.Enabled, TokenHash: row.TokenHash, SigningSecretSealed: row.SigningSecretSealed}, target, nil
}
func webhookAutomationTargetEligible(target sqlc.LockEventWorkflowTargetRow) bool {
	return (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid && api.Plan(target.Plan).WorkflowsAllowed() && !target.MaintenanceMode && !target.PlatformTenantRequired
}
func webhookAutomationDefinitionTx(ctx context.Context, tx pgx.Tx, appID, name string) (*api.WorkflowSpec, string, error) {
	q := sqlc.New()
	live, err := q.LockWorkflowScheduleTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrWebhookAutomationUnavailable
	}
	if err != nil {
		return nil, "", err
	}
	rows, err := q.ListAutomations(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return nil, "", err
	}
	records := make([]Automation, 0, len(rows))
	for _, row := range rows {
		records = append(records, automationFromSQL(row))
	}
	return webhookAutomationDefinition(live.Workflows, records, name, api.Plan(live.Plan))
}
func (s *PgStore) SaveWebhookAutomationBinding(ctx context.Context, opts WebhookAutomationBindingOptions) (WebhookAutomationBinding, error) {
	if err := validateWebhookAutomationBinding(opts); err != nil {
		return WebhookAutomationBinding{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WebhookAutomationBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	endpoint, target, err := lockWebhookAutomationTarget(ctx, tx, opts)
	if err != nil {
		return WebhookAutomationBinding{}, err
	}
	if !webhookAutomationTargetEligible(target) || endpoint.Provider != InboundWebhookProviderStripe {
		return WebhookAutomationBinding{}, ErrWebhookAutomationUnavailable
	}
	q := sqlc.New()
	prior, err := q.GetWebhookAutomationBinding(ctx, tx, mustPgUUID(endpoint.ID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return WebhookAutomationBinding{}, err
	}
	if prior.Version != opts.ExpectedVersion {
		return WebhookAutomationBinding{}, ErrWebhookAutomationConflict
	}
	spec, _, err := webhookAutomationDefinitionTx(ctx, tx, endpoint.AppID, opts.WorkflowName)
	if err != nil {
		return WebhookAutomationBinding{}, err
	}
	if spec == nil {
		return WebhookAutomationBinding{}, ErrNotFound
	}
	if err := validateWorkflowOutboundTx(ctx, tx, endpoint.AppID, endpoint.AccountID, *spec); err != nil {
		return WebhookAutomationBinding{}, err
	}
	row, err := q.SaveWebhookAutomationBinding(ctx, tx, sqlc.SaveWebhookAutomationBindingParams{EndpointID: mustPgUUID(endpoint.ID), WorkflowName: opts.WorkflowName, EventType: opts.EventType, Filter: normalizedWebhookFilter(opts.Filter)})
	if isUniqueViolation(err) {
		return WebhookAutomationBinding{}, ErrWebhookAutomationConflict
	}
	if err != nil {
		return WebhookAutomationBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WebhookAutomationBinding{}, err
	}
	return webhookAutomationBindingFromSQL(row), nil
}
func (s *PgStore) GetWebhookAutomationBinding(ctx context.Context, id string) (WebhookAutomationBinding, error) {
	row, err := sqlc.New().GetWebhookAutomationBinding(ctx, s.pool, mustPgUUID(id))
	return webhookAutomationBindingFromSQL(row), mapErr(err)
}
func (s *PgStore) DeleteWebhookAutomationBinding(ctx context.Context, opts WebhookAutomationBindingOptions) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, err := lockWebhookAutomationTarget(ctx, tx, opts); err != nil {
		return err
	}
	q := sqlc.New()
	row, err := q.GetWebhookAutomationBinding(ctx, tx, mustPgUUID(opts.EndpointID))
	if err != nil {
		return mapErr(err)
	}
	if row.Version != opts.ExpectedVersion {
		return ErrWebhookAutomationConflict
	}
	if _, err := q.DeleteWebhookAutomationBinding(ctx, tx, sqlc.DeleteWebhookAutomationBindingParams{EndpointID: mustPgUUID(opts.EndpointID), Version: opts.ExpectedVersion}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func webhookAutomationReceiptFromSQL(row sqlc.GetWebhookAutomationReceiptRow) (WebhookAutomationReceipt, error) {
	receipt := WebhookAutomationReceipt{EndpointID: pgUUIDString(row.EndpointID), ProviderEventID: row.ProviderEventID, ReceiptID: pgUUIDString(row.ReceiptID), WorkflowName: row.WorkflowName, Status: row.Status, IgnoredReason: row.IgnoredReason.String, AcceptedAt: row.AcceptedAt.Time, outboxID: row.OutboxID, bodyHash: row.BodyHash, RoutingStatus: "ignored"}
	receipt.EventSource = webhookAutomationSource(receipt.EndpointID)
	if row.RecipientID.Valid {
		receipt.recipientID = pgUUIDString(row.RecipientID)
		receipt.RoutingStatus = "pending"
	}
	if row.RunID.Valid {
		receipt.RunID = pgUUIDString(row.RunID)
	}
	var progress map[string]PublishedEventRecipientProgress
	if err := json.Unmarshal(row.RecipientProgress, &progress); err != nil {
		return receipt, err
	}
	if p, ok := progress[receipt.recipientID]; ok {
		receipt.RoutingStatus = p.State
	}
	return receipt, nil
}
func (s *PgStore) GetWebhookAutomationReceipt(ctx context.Context, endpointID, eventID string) (WebhookAutomationReceipt, error) {
	row, err := sqlc.New().GetWebhookAutomationReceipt(ctx, s.pool, sqlc.GetWebhookAutomationReceiptParams{EndpointID: mustPgUUID(endpointID), ProviderEventID: eventID})
	if err != nil {
		return WebhookAutomationReceipt{}, mapErr(err)
	}
	return webhookAutomationReceiptFromSQL(row)
}
func (s *PgStore) AcceptWebhookAutomation(ctx context.Context, verified InboundWebhookEndpoint, body json.RawMessage, runtimeEnabled bool) (WebhookAutomationReceipt, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WebhookAutomationReceipt{}, true, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	endpoint, target, err := lockWebhookAutomationTarget(ctx, tx, WebhookAutomationBindingOptions{EndpointID: verified.ID, AppID: verified.AppID, AccountID: verified.AccountID})
	if err != nil {
		return WebhookAutomationReceipt{}, true, err
	}
	if !endpoint.Enabled || !bytes.Equal(endpoint.SigningSecretSealed, verified.SigningSecretSealed) {
		return WebhookAutomationReceipt{}, true, ErrWebhookAutomationUnavailable
	}
	var event struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return WebhookAutomationReceipt{}, true, ErrAutomationInvalid
	}
	q := sqlc.New()
	row, receiptErr := q.GetWebhookAutomationReceipt(ctx, tx, sqlc.GetWebhookAutomationReceiptParams{EndpointID: mustPgUUID(endpoint.ID), ProviderEventID: event.ID})
	if receiptErr != nil && !errors.Is(receiptErr, pgx.ErrNoRows) {
		return WebhookAutomationReceipt{}, true, receiptErr
	}
	bindingRow, bindingErr := q.GetWebhookAutomationBinding(ctx, tx, mustPgUUID(endpoint.ID))
	if bindingErr != nil && !errors.Is(bindingErr, pgx.ErrNoRows) {
		return WebhookAutomationReceipt{}, true, bindingErr
	}
	if errors.Is(receiptErr, pgx.ErrNoRows) && errors.Is(bindingErr, pgx.ErrNoRows) {
		return WebhookAutomationReceipt{}, false, nil
	}
	if (target.AccountStatus != "active" && target.AccountStatus != "past_due") || target.AbuseHoldAt.Valid || !api.Plan(target.Plan).WorkflowsAllowed() {
		return WebhookAutomationReceipt{}, true, ErrWebhookAutomationUnavailable
	}
	if receiptErr == nil {
		hash, err := webhookAutomationBodyHash(body)
		if err != nil {
			return WebhookAutomationReceipt{}, true, err
		}
		if !bytes.Equal(hash, row.BodyHash) {
			return WebhookAutomationReceipt{}, true, ErrWebhookAutomationConflict
		}
		receipt, err := webhookAutomationReceiptFromSQL(row)
		receipt.Duplicate = true
		return receipt, true, err
	}
	if !runtimeEnabled || !webhookAutomationTargetEligible(target) {
		return WebhookAutomationReceipt{}, true, ErrWebhookAutomationUnavailable
	}
	spec, reason, err := webhookAutomationDefinitionTx(ctx, tx, endpoint.AppID, bindingRow.WorkflowName)
	if err != nil {
		if errors.Is(err, ErrAutomationInvalid) {
			err = ErrWebhookAutomationUnavailable
		}
		return WebhookAutomationReceipt{}, true, err
	}
	receipt, envelope, recipients, err := prepareWebhookAutomation(endpoint, webhookAutomationBindingFromSQL(bindingRow), spec, reason, body, time.Now().UTC())
	if err != nil {
		return receipt, true, err
	}
	legacy, err := q.WebhookAutomationLegacyReceiptExists(ctx, tx, mustPgUUID(receipt.ReceiptID))
	if err != nil {
		return receipt, true, err
	}
	if legacy {
		return WebhookAutomationReceipt{}, false, nil
	}
	if spec != nil && reason == "" {
		if err := validateWorkflowOutboundTx(ctx, tx, endpoint.AppID, endpoint.AccountID, *spec); err != nil {
			return receipt, true, ErrWebhookAutomationUnavailable
		}
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return receipt, true, err
	}
	snapshot, err := json.Marshal(recipients)
	if err != nil {
		return receipt, true, err
	}
	receipt.outboxID, err = q.InsertWebhookAutomationOutbox(ctx, tx, sqlc.InsertWebhookAutomationOutboxParams{AccountID: mustPgUUID(endpoint.AccountID), Source: envelope.Source, EventID: envelope.ID, EventType: envelope.Type, EventData: body, Payload: payload, RecipientSnapshot: snapshot})
	if err != nil {
		return receipt, true, err
	}
	if err := q.AppendEvent(ctx, tx, sqlc.AppendEventParams{Actor: "apid", Kind: "event.published", Subject: mustPgUUID(endpoint.AccountID), Data: payload}); err != nil {
		return receipt, true, err
	}
	recipientID := pgtype.UUID{}
	if receipt.recipientID != "" {
		recipientID = mustPgUUID(receipt.recipientID)
	}
	if err := q.InsertWebhookAutomationReceipt(ctx, tx, sqlc.InsertWebhookAutomationReceiptParams{EndpointID: mustPgUUID(endpoint.ID), ProviderEventID: receipt.ProviderEventID, ReceiptID: mustPgUUID(receipt.ReceiptID), BodyHash: receipt.bodyHash, WorkflowName: receipt.WorkflowName, RecipientID: recipientID, OutboxID: receipt.outboxID, Status: receipt.Status, IgnoredReason: pgtype.Text{String: receipt.IgnoredReason, Valid: receipt.IgnoredReason != ""}}); err != nil {
		return receipt, true, err
	}
	stored, err := q.GetWebhookAutomationReceipt(ctx, tx, sqlc.GetWebhookAutomationReceiptParams{EndpointID: mustPgUUID(endpoint.ID), ProviderEventID: receipt.ProviderEventID})
	if err != nil {
		return receipt, true, err
	}
	receipt, err = webhookAutomationReceiptFromSQL(stored)
	if err != nil {
		return receipt, true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return receipt, true, err
	}
	return receipt, true, nil
}
