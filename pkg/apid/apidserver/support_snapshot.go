package apidserver

// Support ticket snapshots (ADR-20261112210000): when a customer opens a
// ticket, apid saves their account-health view and the refused request
// matching any request id they attached. Capture runs after the ticket is
// committed and is best-effort: a failure is logged and never changes the
// customer's response.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/apid/supportportal"
)

const ticketSnapshotTimeout = 5 * time.Second

// createReceiptWriter passes a ticket-create response through unchanged
// while keeping its status and the first few KiB of its body, enough to
// read the api.SupportSubmission receipt.
type createReceiptWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *createReceiptWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *createReceiptWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if room := 4096 - c.body.Len(); room > 0 {
		if len(b) < room {
			room = len(b)
		}
		c.body.Write(b[:room])
	}
	return c.ResponseWriter.Write(b)
}

func (c *createReceiptWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// serveSupportCreate serves a customer ticket create and then captures the
// snapshot for the ticket it created.
func (s *server) serveSupportCreate(w http.ResponseWriter, r *http.Request, serve func(http.ResponseWriter)) {
	recorder := &createReceiptWriter{ResponseWriter: w}
	serve(recorder)
	if recorder.status < 200 || recorder.status >= 300 {
		return
	}
	var receipt api.SupportSubmission
	if err := json.Unmarshal(recorder.body.Bytes(), &receipt); err != nil || receipt.TicketID == "" {
		return
	}
	s.captureTicketSnapshot(r, receipt.TicketID)
}

// captureTicketSnapshot saves the snapshot for ticketID. The ticket's own
// row decides whose account is captured, so a forged receipt cannot point
// the capture at another account.
func (s *server) captureTicketSnapshot(r *http.Request, ticketID string) {
	if s.customerSupport == nil || s.customerSupport.Pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), ticketSnapshotTimeout)
	defer cancel()
	log := s.log.With("ticket_id", ticketID)
	customer, err := s.customerSupport.TicketCustomer(ctx, ticketID)
	if err != nil || customer.AccountID == "" {
		log.Warn("support snapshot: ticket owner unavailable", "err", err)
		return
	}
	acct, err := s.store.AccountByID(ctx, customer.AccountID)
	if err != nil {
		log.Warn("support snapshot: account unavailable", "err", err)
		return
	}
	now := time.Now().UTC()
	snapshot := supportportal.TicketSnapshot{CapturedAt: now, Plan: string(acct.Plan), Status: string(acct.Status),
		Health: s.accountHealthFor(ctx, r, acct), RequestID: customer.RequestID}
	if customer.RequestID != "" {
		match, err := s.customerErrors.RequestForAccount(ctx, acct.ID, customer.RequestID)
		if err != nil {
			log.Warn("support snapshot: request lookup failed", "err", err)
		}
		snapshot.Request = match
	}
	raw, err := encodeTicketSnapshot(snapshot)
	if err != nil {
		log.Warn("support snapshot: encode failed", "err", err)
		return
	}
	if err := s.customerSupport.SaveTicketSnapshot(ctx, ticketID, now, raw); err != nil {
		log.Warn("support snapshot: save failed", "err", err)
	}
}

// encodeTicketSnapshot marshals a snapshot within support.SnapshotMaxBytes,
// dropping the bulkiest optional detail (recent refused requests, then
// deployment error text) when it would not fit.
func encodeTicketSnapshot(snapshot supportportal.TicketSnapshot) (json.RawMessage, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) <= support.SnapshotMaxBytes {
		return raw, err
	}
	snapshot.Health.RefusedRequests.Recent = []api.AccountHealthRequest{}
	if raw, err = json.Marshal(snapshot); err != nil || len(raw) <= support.SnapshotMaxBytes {
		return raw, err
	}
	for i := range snapshot.Health.FailedDeployments {
		snapshot.Health.FailedDeployments[i].Error, snapshot.Health.FailedDeployments[i].Hint = "", ""
	}
	if raw, err = json.Marshal(snapshot); err != nil || len(raw) <= support.SnapshotMaxBytes {
		return raw, err
	}
	return nil, support.ErrSnapshotTooLarge
}

// loadTicketSnapshot attaches the stored snapshot to a staff panel and
// compares its restrictions with the live diagnosis.
func (s *server) loadTicketSnapshot(ctx context.Context, ticketID string, panel *supportportal.CustomerContext) {
	raw, _, ok, err := s.customerSupport.TicketSnapshot(ctx, ticketID)
	if err != nil || !ok {
		return
	}
	var snapshot supportportal.TicketSnapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return
	}
	panel.Snapshot = &snapshot
	panel.Lifted, panel.Added = restrictionChanges(snapshot.Health.Restrictions, customerAccountHealth(panel.Diagnosis).Restrictions)
}

// restrictionChanges returns the kinds present before but not now (lifted)
// and present now but not before (added), in their original order.
func restrictionChanges(before, now []api.AccountHealthRestriction) (lifted, added []string) {
	had, has := map[string]bool{}, map[string]bool{}
	for _, r := range before {
		had[r.Kind] = true
	}
	for _, r := range now {
		has[r.Kind] = true
	}
	for _, r := range before {
		if !has[r.Kind] {
			lifted = append(lifted, r.Kind)
		}
	}
	for _, r := range now {
		if !had[r.Kind] {
			added = append(added, r.Kind)
		}
	}
	return lifted, added
}
