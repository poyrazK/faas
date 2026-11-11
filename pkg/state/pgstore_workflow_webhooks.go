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

func webhookWorkflowBindingFromSQL(row sqlc.WorkflowWebhookBinding) WebhookWorkflowBinding {
	return WebhookWorkflowBinding{EndpointID: pgUUIDString(row.EndpointID), WorkflowName: row.WorkflowName, EventType: row.EventType, Filter: row.Filter, Version: row.Version, UpdatedAt: row.UpdatedAt.Time}
}
func lockWebhookWorkflowTarget(ctx context.Context, tx pgx.Tx, opts WebhookWorkflowBindingOptions) (InboundWebhookEndpoint, sqlc.LockEventWorkflowTargetRow, error) {
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
	row, err := q.LockWebhookWorkflowEndpoint(ctx, tx, sqlc.LockWebhookWorkflowEndpointParams{ID: mustPgUUID(opts.EndpointID), AppID: mustPgUUID(opts.AppID), AccountID: mustPgUUID(opts.AccountID)})
	if err != nil {
		return InboundWebhookEndpoint{}, target, mapErr(err)
	}
	return InboundWebhookEndpoint{ID: opts.EndpointID, AppID: opts.AppID, AccountID: opts.AccountID, Provider: InboundWebhookProvider(row.Provider), Enabled: row.Enabled, TokenHash: row.TokenHash, SigningSecretSealed: row.SigningSecretSealed}, target, nil
}
func webhookWorkflowTargetEligible(target sqlc.LockEventWorkflowTargetRow) bool {
	return (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid && api.Plan(target.Plan).WorkflowsAllowed() && !target.MaintenanceMode && !target.PlatformTenantRequired
}
func webhookWorkflowDefinitionTx(ctx context.Context, tx pgx.Tx, appID, name string) (*api.WorkflowSpec, string, string, error) {
	q := sqlc.New()
	live, err := q.LockWorkflowScheduleTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", "", ErrWebhookWorkflowUnavailable
	}
	if err != nil {
		return nil, "", "", err
	}
	rows, err := q.ListStoredWorkflows(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return nil, "", "", err
	}
	records := make([]StoredWorkflow, 0, len(rows))
	for _, row := range rows {
		records = append(records, storedWorkflowFromSQL(row))
	}
	spec, reason, err := webhookWorkflowDefinition(live.Workflows, records, name, api.Plan(live.Plan))
	if err == nil {
		paused, _, pauseErr := storedWorkflowFailureBlocksAdmissionPG(ctx, tx, appID, name, time.Now().UTC())
		if pauseErr != nil {
			return nil, "", "", pauseErr
		}
		if paused {
			reason = "stored_workflow_failure_paused"
		}
	}
	return spec, reason, pgUUIDString(live.DeploymentID), err
}
func (s *PgStore) SaveWebhookWorkflowBinding(ctx context.Context, opts WebhookWorkflowBindingOptions) (WebhookWorkflowBinding, error) {
	if err := validateWebhookWorkflowBinding(opts); err != nil {
		return WebhookWorkflowBinding{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WebhookWorkflowBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	endpoint, target, err := lockWebhookWorkflowTarget(ctx, tx, opts)
	if err != nil {
		return WebhookWorkflowBinding{}, err
	}
	if !webhookWorkflowTargetEligible(target) || (endpoint.Provider != InboundWebhookProviderStripe && endpoint.Provider != InboundWebhookProviderGeneric) {
		return WebhookWorkflowBinding{}, ErrWebhookWorkflowUnavailable
	}
	q := sqlc.New()
	prior, err := q.GetWebhookWorkflowBinding(ctx, tx, mustPgUUID(endpoint.ID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return WebhookWorkflowBinding{}, err
	}
	if prior.Version != opts.ExpectedVersion {
		return WebhookWorkflowBinding{}, ErrWebhookWorkflowConflict
	}
	spec, _, _, err := webhookWorkflowDefinitionTx(ctx, tx, endpoint.AppID, opts.WorkflowName)
	if err != nil {
		return WebhookWorkflowBinding{}, err
	}
	if spec == nil {
		return WebhookWorkflowBinding{}, ErrNotFound
	}
	if err := validateWorkflowOutboundTx(ctx, tx, endpoint.AppID, endpoint.AccountID, *spec); err != nil {
		return WebhookWorkflowBinding{}, err
	}
	row, err := q.SaveWebhookWorkflowBinding(ctx, tx, sqlc.SaveWebhookWorkflowBindingParams{EndpointID: mustPgUUID(endpoint.ID), WorkflowName: opts.WorkflowName, EventType: opts.EventType, Filter: normalizedWebhookFilter(opts.Filter)})
	if isUniqueViolation(err) {
		return WebhookWorkflowBinding{}, ErrWebhookWorkflowConflict
	}
	if err != nil {
		return WebhookWorkflowBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WebhookWorkflowBinding{}, err
	}
	return webhookWorkflowBindingFromSQL(row), nil
}
func (s *PgStore) GetWebhookWorkflowBinding(ctx context.Context, id string) (WebhookWorkflowBinding, error) {
	row, err := sqlc.New().GetWebhookWorkflowBinding(ctx, s.pool, mustPgUUID(id))
	return webhookWorkflowBindingFromSQL(row), mapErr(err)
}
func (s *PgStore) DeleteWebhookWorkflowBinding(ctx context.Context, opts WebhookWorkflowBindingOptions) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, err := lockWebhookWorkflowTarget(ctx, tx, opts); err != nil {
		return err
	}
	q := sqlc.New()
	row, err := q.GetWebhookWorkflowBinding(ctx, tx, mustPgUUID(opts.EndpointID))
	if err != nil {
		return mapErr(err)
	}
	if row.Version != opts.ExpectedVersion {
		return ErrWebhookWorkflowConflict
	}
	if _, err := q.DeleteWebhookWorkflowBinding(ctx, tx, sqlc.DeleteWebhookWorkflowBindingParams{EndpointID: mustPgUUID(opts.EndpointID), Version: opts.ExpectedVersion}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func webhookWorkflowReceiptFromSQL(row sqlc.GetWebhookWorkflowReceiptRow, provider InboundWebhookProvider) (WebhookWorkflowReceipt, error) {
	receipt := WebhookWorkflowReceipt{EndpointID: pgUUIDString(row.EndpointID), ProviderEventID: row.ProviderEventID, ReceiptID: pgUUIDString(row.ReceiptID), WorkflowName: row.WorkflowName, Status: row.Status, IgnoredReason: row.IgnoredReason.String, AcceptedAt: row.AcceptedAt.Time, outboxID: row.OutboxID, bodyHash: row.BodyHash, RoutingStatus: "ignored"}
	receipt.EventSource = webhookWorkflowSource(provider, receipt.EndpointID)
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
func (s *PgStore) GetWebhookWorkflowReceipt(ctx context.Context, endpointID, eventID string) (WebhookWorkflowReceipt, error) {
	row, err := sqlc.New().GetWebhookWorkflowReceipt(ctx, s.pool, sqlc.GetWebhookWorkflowReceiptParams{EndpointID: mustPgUUID(endpointID), ProviderEventID: eventID})
	if err != nil {
		return WebhookWorkflowReceipt{}, mapErr(err)
	}
	endpoint, err := s.InboundWebhookEndpointByID(ctx, endpointID)
	if err != nil {
		return WebhookWorkflowReceipt{}, err
	}
	return webhookWorkflowReceiptFromSQL(row, endpoint.Provider)
}
func (s *PgStore) AcceptWebhookWorkflow(ctx context.Context, verified InboundWebhookEndpoint, body json.RawMessage, runtimeEnabled bool) (WebhookWorkflowReceipt, bool, error) {
	eventID, eventType, err := webhookEventFromStripeBody(body)
	if err != nil {
		return WebhookWorkflowReceipt{}, true, err
	}
	return s.AcceptVerifiedWebhookWorkflow(ctx, verified, eventID, eventType, body, runtimeEnabled)
}
func (s *PgStore) AcceptVerifiedWebhookWorkflow(ctx context.Context, verified InboundWebhookEndpoint, eventID, eventType string, body json.RawMessage, runtimeEnabled bool) (WebhookWorkflowReceipt, bool, error) {
	if len(eventID) == 0 || len(eventID) > api.WorkflowWebhookEventMaxBytes ||
		(eventType != "" && !api.ValidInboundWebhookEventType(eventType)) ||
		(verified.Provider == InboundWebhookProviderGeneric && (!api.ValidInboundWebhookEventType(eventType) || !api.ValidInboundWebhookEventID(eventID))) {
		return WebhookWorkflowReceipt{}, true, ErrStoredWorkflowInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WebhookWorkflowReceipt{}, true, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	endpoint, target, err := lockWebhookWorkflowTarget(ctx, tx, WebhookWorkflowBindingOptions{EndpointID: verified.ID, AppID: verified.AppID, AccountID: verified.AccountID})
	if err != nil {
		return WebhookWorkflowReceipt{}, true, err
	}
	if !endpoint.Enabled || !bytes.Equal(endpoint.SigningSecretSealed, verified.SigningSecretSealed) {
		return WebhookWorkflowReceipt{}, true, ErrWebhookWorkflowUnavailable
	}
	q := sqlc.New()
	row, receiptErr := q.GetWebhookWorkflowReceipt(ctx, tx, sqlc.GetWebhookWorkflowReceiptParams{EndpointID: mustPgUUID(endpoint.ID), ProviderEventID: eventID})
	if receiptErr != nil && !errors.Is(receiptErr, pgx.ErrNoRows) {
		return WebhookWorkflowReceipt{}, true, receiptErr
	}
	bindingRow, bindingErr := q.GetWebhookWorkflowBinding(ctx, tx, mustPgUUID(endpoint.ID))
	if bindingErr != nil && !errors.Is(bindingErr, pgx.ErrNoRows) {
		return WebhookWorkflowReceipt{}, true, bindingErr
	}
	if errors.Is(receiptErr, pgx.ErrNoRows) && errors.Is(bindingErr, pgx.ErrNoRows) {
		return WebhookWorkflowReceipt{}, false, nil
	}
	if (target.AccountStatus != "active" && target.AccountStatus != "past_due") || target.AbuseHoldAt.Valid || !api.Plan(target.Plan).WorkflowsAllowed() {
		return WebhookWorkflowReceipt{}, true, ErrWebhookWorkflowUnavailable
	}
	if receiptErr == nil {
		hash, err := webhookWorkflowBodyHashForEvent(endpoint.Provider, eventType, body)
		if err != nil {
			return WebhookWorkflowReceipt{}, true, err
		}
		if !bytes.Equal(hash, row.BodyHash) {
			return WebhookWorkflowReceipt{}, true, ErrWebhookWorkflowConflict
		}
		receipt, err := webhookWorkflowReceiptFromSQL(row, endpoint.Provider)
		receipt.Duplicate = true
		return receipt, true, err
	}
	if !runtimeEnabled || !webhookWorkflowTargetEligible(target) {
		return WebhookWorkflowReceipt{}, true, ErrWebhookWorkflowUnavailable
	}
	spec, reason, deploymentID, err := webhookWorkflowDefinitionTx(ctx, tx, endpoint.AppID, bindingRow.WorkflowName)
	if err != nil {
		if errors.Is(err, ErrStoredWorkflowInvalid) {
			err = ErrWebhookWorkflowUnavailable
		}
		return WebhookWorkflowReceipt{}, true, err
	}
	receipt, envelope, recipients, err := prepareWebhookWorkflow(endpoint, webhookWorkflowBindingFromSQL(bindingRow), spec, reason, eventID, eventType, body, time.Now().UTC())
	if err != nil {
		return receipt, true, err
	}
	legacy, err := q.WebhookWorkflowLegacyReceiptExists(ctx, tx, mustPgUUID(receipt.ReceiptID))
	if err != nil {
		return receipt, true, err
	}
	if legacy {
		return WebhookWorkflowReceipt{}, false, nil
	}
	if spec != nil && reason == "" {
		if err := validateWorkflowOutboundTx(ctx, tx, endpoint.AppID, endpoint.AccountID, *spec); err != nil {
			return receipt, true, ErrWebhookWorkflowUnavailable
		}
	}
	for i := range recipients {
		recipients[i].DeploymentID = deploymentID
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return receipt, true, err
	}
	snapshot, err := json.Marshal(recipients)
	if err != nil {
		return receipt, true, err
	}
	receipt.outboxID, err = q.InsertWebhookWorkflowOutbox(ctx, tx, sqlc.InsertWebhookWorkflowOutboxParams{AccountID: mustPgUUID(endpoint.AccountID), Source: envelope.Source, EventID: envelope.ID, EventType: envelope.Type, EventData: body, Payload: payload, RecipientSnapshot: snapshot})
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
	if err := q.InsertWebhookWorkflowReceipt(ctx, tx, sqlc.InsertWebhookWorkflowReceiptParams{EndpointID: mustPgUUID(endpoint.ID), ProviderEventID: receipt.ProviderEventID, ReceiptID: mustPgUUID(receipt.ReceiptID), BodyHash: receipt.bodyHash, WorkflowName: receipt.WorkflowName, RecipientID: recipientID, OutboxID: receipt.outboxID, Status: receipt.Status, IgnoredReason: pgtype.Text{String: receipt.IgnoredReason, Valid: receipt.IgnoredReason != ""}}); err != nil {
		return receipt, true, err
	}
	stored, err := q.GetWebhookWorkflowReceipt(ctx, tx, sqlc.GetWebhookWorkflowReceiptParams{EndpointID: mustPgUUID(endpoint.ID), ProviderEventID: receipt.ProviderEventID})
	if err != nil {
		return receipt, true, err
	}
	receipt, err = webhookWorkflowReceiptFromSQL(stored, endpoint.Provider)
	if err != nil {
		return receipt, true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return receipt, true, err
	}
	return receipt, true, nil
}
