package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productemail"
)

func productEmailUUID(v string) bool {
	id, err := uuid.Parse(v)
	return err == nil && id != uuid.Nil && id.String() == v
}
func validateProductEmailCommand(c productCommand) error {
	if c.tenant != "" && !productEmailUUID(c.tenant) {
		return errors.New("--tenant requires a canonical customer UUID")
	}
	if c.verb == "email recoveries" || c.verb == "email opt-outs" {
		if len(c.cursor) > api.ProductControlOwnerCursorMaxBytes {
			return errors.New("use the bounded notification cursor returned by history")
		}
		return nil
	}
	if c.verb == "email list" {
		if !productemail.OwnerStatus(c.emailStatus) || len(c.cursor) > api.ProductControlOwnerCursorMaxBytes {
			return errors.New("use a supported --status and the bounded --cursor returned by delivery history")
		}
		return nil
	}
	if c.tenant == "" || !productEmailUUID(c.deliveryID) || c.failedAttempt < 1 || c.failedAttempt >= api.ProductControlEmailMaxAttempts {
		return errors.New("--tenant, --delivery and --attempt require the customer UUID, delivery UUID and eligible failed attempt from email history")
	}
	return nil
}
func runProductEmailCommand(ctx context.Context, client *Client, c productCommand) (any, error) {
	if c.verb == "email opt-outs" {
		page, err := client.ListProductEmailOptOuts(ctx, c.app, c.tenant, c.cursor)
		if err == nil {
			err = productemail.ValidateOptOutPage(page, c.tenant, c.cursor)
		}
		return page, err
	}
	if c.verb == "email recoveries" {
		page, err := client.ListProductEmailRecoveries(ctx, c.app, c.tenant, c.cursor)
		if err == nil {
			err = productemail.ValidateRecoveryPage(page, c.tenant, c.cursor)
		}
		return page, err
	}
	if c.verb == "email list" {
		page, err := client.ListProductEmailDeliveries(ctx, c.app, c.tenant, c.emailStatus, c.cursor)
		if err == nil {
			err = productemail.ValidateOwnerPage(page, c.tenant, c.emailStatus, c.cursor)
		}
		return page, err
	}
	r, err := client.RetryProductEmailDelivery(ctx, c.app, c.deliveryID, api.ProductEmailRetryRequest{TenantID: c.tenant, ExpectedAttempts: &c.failedAttempt})
	if err == nil && (r.DeliveryID != c.deliveryID || r.TenantID != c.tenant || r.FailedAttempt != c.failedAttempt || r.QueuedAt.IsZero()) {
		err = errors.New("email retry receipt does not match the requested delivery, customer and failed attempt")
	}
	return r, err
}
func writeProductEmailOutput(out io.Writer, value any) (bool, error) {
	if p, ok := value.(api.ProductEmailOptOutPage); ok {
		return true, writeProductEmailOptOuts(out, p)
	}
	if p, ok := value.(api.ProductEmailRecoveryPage); ok {
		return true, writeProductEmailRecoveries(out, p)
	}
	if r, ok := value.(api.ProductEmailRetryReceipt); ok {
		_, err := fmt.Fprintf(out, "Retry queued: delivery %s · failed attempt %d · customer %s\nQueued at %s. This acknowledges a requeue; the worker rechecks current access before sending.\nRepeat the same --delivery, --tenant and --attempt to recover this receipt.\n", r.DeliveryID, r.FailedAttempt, r.TenantID, r.QueuedAt.UTC().Format(time.RFC3339))
		return true, err
	}
	p, ok := value.(api.ProductEmailOwnerPage)
	if !ok {
		return false, nil
	}
	c := p.Counts
	if _, err := fmt.Fprintf(out, "Notification email · mode %s · observed %s\nRetained since %s · queued %d · sending %d · captured locally %d · provider accepted %d · failed %d · cancelled %d\nCounts cover every status in the selected customer/product window. Provider acceptance does not verify inbox delivery.\n", p.Mode, p.ObservedAt.UTC().Format(time.RFC3339), p.WindowStart.UTC().Format(time.RFC3339), c.Queued, c.Leased, c.Captured, c.ProviderAccepted, c.Failed, c.Cancelled); err != nil {
		return true, err
	}
	if _, err := fmt.Fprintf(out, "Provider reports: delivered %d · bounced %d · complained %d. Delivery reports do not prove inbox placement or reading.\n", c.Delivered, c.Bounced, c.Complained); err != nil {
		return true, err
	}
	if p.Mode == "local" {
		if _, err := fmt.Fprintln(out, "Local capture only; no email is sent."); err != nil {
			return true, err
		}
	}
	if p.OldestPendingAt != nil {
		if _, err := fmt.Fprintf(out, "Oldest pending: %s old · %s\n", max(time.Duration(0), p.ObservedAt.Sub(*p.OldestPendingAt)).Truncate(time.Second), p.OldestPendingAt.UTC().Format(time.RFC3339)); err != nil {
			return true, err
		}
	}
	for _, d := range p.Deliveries {
		if _, err := fmt.Fprintf(out, "\n%s · %s · attempts %d\nDelivery %s · customer %s\nNotification %s · recipient reference %s\n%s\n%s\nDeadline %s\n", productemail.StatusLabel(d.Status), d.Kind, d.Attempts, d.ID, d.TenantID, d.NotificationID, d.RecipientID, productemail.ReasonLabel(d.Reason), productemail.RetryLabel(d), d.Deadline.UTC().Format(time.RFC3339)); err != nil {
			return true, err
		}
		if d.PausedAt != nil {
			if _, err := fmt.Fprintf(out, "Subscription paused at: %s\n", d.PausedAt.UTC().Format(time.RFC3339)); err != nil {
				return true, err
			}
		}
		if d.ReportedAt != nil {
			if _, err := fmt.Fprintf(out, "Provider reported at: %s\n", d.ReportedAt.UTC().Format(time.RFC3339)); err != nil {
				return true, err
			}
		}
		if d.Status == "queued" {
			if _, err := fmt.Fprintf(out, "Next attempt: %s\n", d.NextAttemptAt.UTC().Format(time.RFC3339)); err != nil {
				return true, err
			}
		}
	}
	if len(p.Deliveries) == 0 {
		if _, err := fmt.Fprintln(out, "No retained messages match this view."); err != nil {
			return true, err
		}
	}
	if p.NextCursor != "" {
		_, err := fmt.Fprintln(out, "Older messages: --cursor "+p.NextCursor)
		return true, err
	}
	return true, nil
}

func writeProductEmailOptOuts(out io.Writer, p api.ProductEmailOptOutPage) error {
	if _, err := fmt.Fprintln(out, "Notification opt-outs · historical confirmations; later authenticated consent is checked separately."); err != nil {
		return err
	}
	for _, r := range p.Receipts {
		if _, err := fmt.Fprintf(out, "Opt-out %s · customer %s · recipient reference %s\nConfirmed %s · preference version %d → %d · generation %d · email %s\n", r.ID, r.TenantID, r.RecipientID, r.UnsubscribedAt.UTC().Format(time.RFC3339), r.ExpectedVersion, r.Version, r.Generation, r.DeliveryID); err != nil {
			return err
		}
	}
	if len(p.Receipts) == 0 {
		if _, err := fmt.Fprintln(out, "No retained notification opt-outs."); err != nil {
			return err
		}
	}
	if p.NextCursor != "" {
		_, err := fmt.Fprintln(out, "Older opt-outs: --cursor "+p.NextCursor)
		return err
	}
	return nil
}

func writeProductEmailRecoveries(out io.Writer, p api.ProductEmailRecoveryPage) error {
	if _, err := fmt.Fprintln(out, "Notification recoveries · confirmations enabled only future updates; check current pause/delivery state separately."); err != nil {
		return err
	}
	for _, r := range p.Receipts {
		if _, err := fmt.Fprintf(out, "Recovery %s · customer %s · recipient reference %s\nConfirmed %s · generation %d → %d · preference version %d → %d\n", r.ID, r.TenantID, r.RecipientID, r.RecoveredAt.UTC().Format(time.RFC3339), r.PreviousGeneration, r.Generation, r.ExpectedVersion, r.Version); err != nil {
			return err
		}
	}
	if len(p.Receipts) == 0 {
		if _, err := fmt.Fprintln(out, "No retained notification recoveries."); err != nil {
			return err
		}
	}
	if p.NextCursor != "" {
		_, err := fmt.Fprintln(out, "Older recoveries: --cursor "+p.NextCursor)
		return err
	}
	return nil
}
