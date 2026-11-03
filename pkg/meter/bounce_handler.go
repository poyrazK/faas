package meter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// MailBounce is the inbound event the apid webhook handler hands
// the bounce handler after a Resend / Postmark bounce webhook
// (cmd/apid/handlers_mail_webhooks.go, C8) has authenticated and
// normalized the payload. The handler is transport-agnostic so
// Postmark parity is a follow-up that calls the same entry point.
type MailBounce struct {
	// Source identifies the provider (resend / postmark).
	// Mirrors state.MailSuppressionSource — the bounce handler
	// does not import pkg/state directly so this field stays
	// transport-shaped; the apid webhooks map it onto the
	// state.* enum at the call site.
	Source string
	// ProviderEventID is the upstream event id. The (source,
	// provider_event_id) pair is the unique dedupe key on
	// mail_suppressions, so a webhook redelivery is a single
	// INSERT … ON CONFLICT DO UPDATE.
	ProviderEventID string
	// Email is the recipient that bounced. Lower-cased before
	// suppression lookup.
	Email string
	// Reason is the bounce classification. Today the closed set
	// is "hard_bounce" (the address is permanently invalid) and
	// "complaint" (the recipient hit "spam"). soft_bounces are
	// intentionally NOT processed — Resend's free tier retries
	// transient failures itself, and a soft-bounce-as-past_due
	// would flip paying customers off the platform on a flaky
	// SMTP path.
	Reason string
	// OccurredAt is when the provider recorded the event. Nil
	// falls back to time.Now() at handler entry.
	OccurredAt *time.Time
}

// BounceAuditor is the minimal audit seam the bounce handler
// needs — pkg/audit.Auditor is the prod implementation, but
// tests inject a recordingAuditor without dragging the
// full Ops / store wiring into the test path. Keeping the
// interface in the consumer package mirrors the
// pkg/mail.SuppressionChecker pattern (C5): the leaf package
// owns its seam so the dependency direction stays clean.
type BounceAuditor interface {
	Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
}

// BounceHandler is the consumer-side of the mail-suppression +
// dunning pipeline (issue #246 acceptance item 7, ADR-115 §D.3).
// One instance per meterd process; constructed in cmd/meterd/main.go
// and called from the apid webhook ingress via gRPC / pg_notify
// (the cross-process plumbing is C8 — see plan).
type BounceHandler struct {
	Store   state.Store
	Auditor BounceAuditor
	Log     *slog.Logger
}

// HandleMailBounce is the single entry point. Behaviour by Reason:
//
//	hard_bounce:
//	  1. Look up the account by email. nil accountID is fine —
//	     a bounce on an address the platform has no record of
//	     still goes on the suppression list so future mail to
//	     that address is dropped at the decorator.
//	  2. Write a mail_suppressions row. The (source, event_id)
//	     unique index dedupes replays; the bool returned by
//	     RecordMailSuppression tells the handler whether this
//	     was a fresh insert (true) or a replay (false).
//	  3. Emit a mail.bounce_hard audit row.
//	  4. If the suppression was a fresh insert AND the account
//	     is currently active, advance dunning via
//	     MarkDunningStep(active, past_due). This stamps
//	     past_due_at and starts the existing 7-day timer. The
//	     CAS ErrNotFound means the account is already past_due /
//	     suspended / deleted_pending — the redelivery-race
//	     guard the existing handler relies on.
//
//	complaint:
//	  1+2+3. Same suppression + audit row.
//	  4. Do NOT advance dunning. Suspending an account because
//	     the recipient hit "spam" is hostile: spam is a
//	     feedback signal the provider cares about, not a
//	     billing failure.
//
//	(any other reason):
//	  Return ErrMailBounceIgnored without writing anything.
//	  Soft bounces are not handled today (see MailBounce.Reason
//	  doc).
func (h *BounceHandler) HandleMailBounce(ctx context.Context, b MailBounce) error {
	if h == nil || h.Store == nil {
		return errors.New("meter: BounceHandler: Store is nil")
	}
	if h.Auditor == nil {
		return errors.New("meter: BounceHandler: Auditor is nil")
	}
	if strings.TrimSpace(b.Email) == "" {
		return errors.New("meter: MailBounce.Email is empty")
	}
	if strings.TrimSpace(b.ProviderEventID) == "" {
		return errors.New("meter: MailBounce.ProviderEventID is empty")
	}
	if b.Source == "" {
		return errors.New("meter: MailBounce.Source is empty")
	}

	switch b.Reason {
	case "hard_bounce", "complaint":
		// fall through
	default:
		// Soft bounces + unknown reasons: ignore silently. The
		// webhook handler has already ack'd the provider with
		// 200 so Resend stops retrying; we don't suppress or
		// audit because the address may recover and a
		// soft_bounce-as-past_due would be a false positive.
		if h.Log != nil {
			h.Log.Debug("meter: bounce ignored",
				"reason", b.Reason,
				"email", b.Email,
				"event_id", b.ProviderEventID)
		}
		return ErrMailBounceIgnored
	}

	occurred := time.Now().UTC()
	if b.OccurredAt != nil {
		occurred = b.OccurredAt.UTC()
	}

	// Look up the account by email. ErrNotFound is a normal
	// outcome — the suppression still lands, the dunning step
	// does not.
	var accountPtr *string
	acct, acctErr := h.Store.AccountByEmail(ctx, strings.ToLower(b.Email))
	if acctErr == nil {
		idStr := acct.ID
		accountPtr = &idStr
	} else if !errors.Is(acctErr, state.ErrNotFound) {
		// A non-NotFound error is a real DB failure — do not
		// silently lose the suppression, but also do not block
		// the dunning step on a flaky read. Log and continue
		// with no accountPtr (suppression still writes).
		if h.Log != nil {
			h.Log.Warn("meter: bounce AccountByEmail failed",
				"email", b.Email, "err", acctErr)
		}
	} else if h.Log != nil {
		// Bounce on an address we have no record of — log at
		// debug so a campaign / newsletter regression is
		// visible without spamming the warn channel.
		h.Log.Debug("meter: bounce email not on file",
			"email", b.Email, "event_id", b.ProviderEventID)
	}

	// Step 1: write the suppression row. RecordMailSuppression
	// returns inserted=false on a (source, event_id) replay —
	// the bounce handler MUST NOT advance dunning on a replay
	// or a webhook redelivery would mark the customer past_due
	// twice. The SQLSTATE 23505 → ErrConflict path is reserved
	// for callers that want strict failure; we tolerate the
	// conflict and treat it as a no-op.
	sup := state.MailSuppressionInput{
		AccountID:       accountPtr,
		Email:           b.Email,
		Reason:          state.MailSuppressionReason(b.Reason),
		Source:          state.MailSuppressionSource(b.Source),
		ProviderEventID: b.ProviderEventID,
	}
	inserted, err := h.Store.RecordMailSuppression(ctx, sup)
	if err != nil {
		// ErrConflict is the SQLSTATE 23505 path the (source,
		// provider_event_id) UNIQUE index raises when two near-
		// simultaneous webhook deliveries race past the
		// webhookdedupe layer and reach the INSERT together.
		// The bounce handler's documented contract is "we
		// tolerate the conflict and treat it as a no-op" (the
		// 00535 migration doc says the same), so the bounce
		// handler must NOT bubble ErrConflict to apid — the
		// webhook ingress would 500 and Resend would retry,
		// hitting the same conflict in a loop.
		//
		// Treated as a replay: no dunning step, no second audit
		// row. We log the conflict at debug so a reappearing
		// race condition is visible to an SRE without spamming
		// the warn channel (the per-event replay is a normal
		// outcome, not a failure mode).
		if errors.Is(err, state.ErrConflict) {
			if h.Log != nil {
				h.Log.Debug("meter: bounce suppression replay (conflict)",
					"event_id", b.ProviderEventID,
					"source", b.Source,
					"email", b.Email)
			}
			return nil
		}
		return fmt.Errorf("meter: RecordMailSuppression: %w", err)
	}

	// Step 2: audit row. mail.bounce_hard or mail.bounce_complaint
	// — a closed prefix the dashboard groups by so a cluster of
	// bounces from one customer is visible at a glance.
	kind := auditKindForBounce(b.Reason)
	if h.Auditor != nil {
		h.Auditor.Emit(ctx, kind, accountPtr, map[string]any{
			"source":            b.Source,
			"provider_event_id": b.ProviderEventID,
			"email":             b.Email,
			"occurred_at":       occurred.Format(time.RFC3339Nano),
			"inserted":          inserted,
		})
	}

	// A bounce never moves an account's billing status. It used to mark an
	// active account past_due, so one bounce on a removed work mailbox
	// suspended a paying customer a week later and scheduled a Free account
	// for deletion with its databases, while the notices went to the
	// suppressed address (bug hunt 2026-10-28 B26). Dunning follows billing
	// events only; the suppression above marks the address undeliverable.
	if h.Log != nil && b.Reason == "hard_bounce" && inserted && accountPtr != nil {
		h.Log.Warn("meter: account email bounced; address suppressed",
			"account", *accountPtr, "event_id", b.ProviderEventID)
	}
	return nil
}

// ErrMailBounceIgnored is returned for bounces the handler
// deliberately does not act on (soft_bounces and unknown reasons).
// The webhook handler treats this as 200 so the provider stops
// retrying.
var ErrMailBounceIgnored = errors.New("meter: mail bounce ignored")

// NewLocalBounceHandler wires the bounce handler against the
// local state.Store + BounceAuditor. Used by cmd/apid so the
// /v1/webhooks/resend route can dispatch into the suppression +
// dunning pipeline without an RPC roundtrip (the meterd process
// is co-located with apid on a single control-plane box today).
// The seam takes a BounceAuditor interface (not *audit.Auditor)
// so test code can inject a recordingAuditor — same consumer-
// package pattern pkg/mail.SuppressionChecker uses.
func NewLocalBounceHandler(store state.Store, aud BounceAuditor, log *slog.Logger) *BounceHandler {
	return &BounceHandler{Store: store, Auditor: aud, Log: log}
}

// auditKindForBounce maps the closed-set Reason to the audit
// kind string. Kept inline so a typo in the kind is caught by
// the bounce handler tests rather than discovered by an SRE
// staring at Grafana.
func auditKindForBounce(reason string) string {
	switch reason {
	case "hard_bounce":
		return "mail.bounce_hard"
	case "complaint":
		return "mail.bounce_complaint"
	default:
		// Unknown reasons never reach this function (the
		// switch in HandleMailBounce returns earlier), but a
		// future addition lands here without a panic.
		return "mail.bounce_unknown"
	}
}
