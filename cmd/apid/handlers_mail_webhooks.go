package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/webhookdedupe"
)

// mailBounceHandler is the apid-side seam for the mail-suppression
// + dunning pipeline (issue #246 acceptance item 8). cmd/apid wires
// this to the meterd-owned *meter.BounceHandler via WithMailBounce
// in main.go. The interface lives in the consumer package so test
// code can inject a fake without dragging pkg/meter into the test
// path — same pattern as SuppressionChecker (C5) and BounceAuditor
// (C7). The signature mirrors meter.BounceHandler.HandleMailBounce
// exactly so the prod wiring is a thin adapter.
type mailBounceHandler interface {
	HandleMailBounce(ctx context.Context, b meter.MailBounce) error
}

// resendEvent is the subset of Resend's webhook envelope the apid
// handler needs. Resend's full payload is documented at
// https://resend.com/docs/webhooks/introduction but the handler
// only reads `type` + `data.email`; everything else (created_at,
// recipient, ...) is forwarded as-is into the audit row when we
// have it. Keeping the struct narrow avoids a second bind of the
// provider's full schema (which is documented to grow).
type resendEvent struct {
	Type string `json:"type"`
	Data struct {
		Email     string `json:"email"`
		CreatedAt string `json:"created_at"`
	} `json:"data"`
}

// resendReason maps Resend's `type` field onto the closed
// MailBounce.Reason set the bounce handler accepts. Anything we
// can't classify falls through as "" and the bounce handler
// returns ErrMailBounceIgnored → apid returns 200 so Resend
// stops retrying.
func resendReason(t string) string {
	switch t {
	case "email.bounced":
		return "hard_bounce"
	case "email.complained":
		return "complaint"
	default:
		// email.delivered, email.delivery_delayed, email.opened,
		// email.clicked — these are observational events. The
		// bounce handler ignores unknown reasons (returns
		// ErrMailBounceIgnored) so we map them to "" to take
		// that branch.
		return ""
	}
}

// resendWebhook accepts signed Resend bounce / complaint events
// (issue #246 acceptance item 8). The HMAC envelope is Svix /
// Standard Webhooks (verified by pkg/mail.VerifyResendSignature);
// replay is guarded by the durable webhook delivery claim using the svix-id
// delivery UUID; the resulting bounce is dispatched to the
// mailBounceHandler the meterd process registered via main.go.
//
// Returns 503 if the secret is unset (fail-closed — missing
// FAAS_MAIL_RESEND_WEBHOOK_SECRET cannot silently accept unsigned
// events), 400 on bad signature / unparseable body, 200 on
// success, replay, or unhandled event type so Resend stops
// retrying. Mirrors stripeWebhook's behaviour at
// cmd/apid/handlers_ext.go:3267 — the HMAC IS the trust boundary,
// so the route is mounted unwrapped (no auth middleware).
func (s *server) resendWebhook(w http.ResponseWriter, r *http.Request) {
	// PR #1191 fixup: bound the inbound body at api.WebhookMaxBodyBytes
	// (1 MiB) so an unauthenticated sender cannot OOM apid by streaming
	// multi-GB garbage. Resend payloads are <4 KB; the 256× headroom is
	// for future event-type expansion. MaxBytesReader returns
	// *http.MaxBytesError when the cap is hit, which io.ReadAll
	// surfaces as a plain error; we map both shapes to 400 below.
	r.Body = http.MaxBytesReader(w, r.Body, api.WebhookMaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad webhook", err.Error()))
		return
	}
	if s.resendWebhookSecret == "" {
		s.log.Error("resend_webhook.no_secret",
			"err", "FAAS_MAIL_RESEND_WEBHOOK_SECRET is unset; refusing to process events")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"resend webhook not configured",
			"FAAS_MAIL_RESEND_WEBHOOK_SECRET is unset; refusing to process events"))
		return
	}
	if err := mail.VerifyResendSignature(body,
		r.Header.Get(mail.ResendSignatureHeader),
		s.resendWebhookSecret,
		r.Header.Get(mail.ResendIDHeader),
		r.Header.Get(mail.ResendTimestampHeader),
		mail.DefaultResendSignatureTolerance,
	); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad signature", err.Error()))
		return
	}

	var ev resendEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad webhook", err.Error()))
		return
	}

	// Replay check — svix-id is the delivery UUID Resend uses.
	// The dedupe window is 5 minutes (webhookdedupe.TTL). On a
	// hit, emit the audit row + 200 so Resend stops retrying.
	deliveryID := r.Header.Get(mail.ResendIDHeader)
	if deliveryID == "" {
		api.WriteProblem(w, api.ErrValidation("resend webhook delivery id is required"))
		return
	}
	claimed, err := s.claimWebhookDelivery(r.Context(), webhookdedupe.ProviderResend, deliveryID)
	if err != nil {
		s.log.Error("resend replay claim failed", "delivery_id", logsanitize.Field(deliveryID), "err", err)
		api.WriteProblem(w, api.ErrCapacity("mail webhook replay protection unavailable"))
		return
	}
	if !claimed {
		s.audit.Emit(r.Context(), "webhook.replay_rejected", nil, map[string]any{
			"provider":    webhookdedupe.ProviderResend,
			"delivery_id": logsanitize.Field(deliveryID),
		})
		w.WriteHeader(http.StatusOK)
		return
	}

	reason := resendReason(ev.Type)
	if reason == "" {
		// Unhandled event type — observability events (delivered,
		// opened, clicked) — ack 200 so Resend stops retrying.
		// The bounce handler would have returned
		// ErrMailBounceIgnored anyway.
		w.WriteHeader(http.StatusOK)
		return
	}

	if s.mailBounce == nil {
		// The bounce handler is wired in cmd/apid/main.go after
		// the state store + audit auditor are loaded. If we got
		// here without it the wiring is broken — fail loud so
		// the operator sees a 500 rather than silently dropping
		// bounces.
		s.releaseWebhookDelivery(r.Context(), webhookdedupe.ProviderResend, deliveryID)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"mail bounce handler not wired",
			"resend webhook received but no mailBounce handler is configured"))
		return
	}

	bounce := meter.MailBounce{
		Source:          "resend",
		ProviderEventID: r.Header.Get(mail.ResendIDHeader),
		Email:           ev.Data.Email,
		Reason:          reason,
	}
	if err := s.mailBounce.HandleMailBounce(r.Context(), bounce); err != nil {
		// The bounce handler returns ErrMailBounceIgnored for
		// soft_bounces / unknown reasons — treat as 200 so
		// Resend stops retrying. Any other error is a real
		// failure (DB outage, missing Auditor, …) and bubbles
		// to a 500 + journal.
		if errors.Is(err, meter.ErrMailBounceIgnored) {
			w.WriteHeader(http.StatusOK)
			return
		}
		s.log.Error("resend_webhook.bounce_handler_failed",
			"err", err, "delivery_id", logsanitize.Field(bounce.ProviderEventID))
		s.releaseWebhookDelivery(r.Context(), webhookdedupe.ProviderResend, deliveryID)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Bounce handler failed", err.Error()))
		return
	}
	w.WriteHeader(http.StatusOK)
}
