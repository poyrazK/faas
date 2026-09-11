// gatewayd-internal → githubd webhook proxy (spec §14 M7.5, ADR-012).
//
// gatewayd-public forwards /webhooks/github over the unix socket at
// /run/faas/gatewayd-internal.sock; the request lands here behind the
// we HMAC-verify the GitHub push header at the edge (the secret never
// has to leave gatewayd-internal's config), then reverse-proxy the request to
// githubd's loopback listener (127.0.0.1:8083 by default).
//
// githubd stays loopback-only so the §11 single-public-listener
// invariant survives. This proxy is the only way GitHub's POST
// reaches githubd's webhook handler.
//
// Issue #294: after HMAC-verify, we consult the shared webhook
// dedupe table via pkg/webhookdedupe to reject replays within the
// 5-minute TTL window with 200 (idempotent — GitHub interprets as
// success and stops retrying) and emit a webhook.replay_rejected
// audit row.
package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/webhookdedupe"
)

// githubWebhookPath is the URL GitHub POSTs to (one webhook per app
// binding; today we use the catch-all path that githubd then
// routes per-binding from the repo field in the body).
const githubWebhookPath = "/webhooks/github"

// githubdProxy wraps next so /webhooks/github requests are
// HMAC-verified at the edge and forwarded to githubd's loopback
// listener. Everything else falls through to next (dashboard proxy
// → apid, or gateway.Handler's wake/route proxy).
type githubdProxy struct {
	target    *url.URL
	secret    []byte
	next      http.Handler
	log       *slog.Logger
	transport *http.Transport
	auditor   *gatewaydAuditor
	replay    webhookReplayStore
}

type webhookReplayStore interface {
	ClaimWebhookDelivery(context.Context, string, string, time.Time, time.Time) (bool, error)
	ReleaseWebhookDelivery(context.Context, string, string) error
}

// newGithubdProxy builds the proxy. If target is empty or secret
// is missing, the wrapper is disabled (every /webhooks/github
// request returns 503 — gatewayd-internal refuses to forward unverified
// payloads, so missing secret = closed-by-default).
//
// auditor may be nil; in that case replay rejections are still
// 200-returned but no audit row is emitted. Tests for replay
// wiring install an auditor fake.
//
// Production passes a durable webhookReplayStore so protection survives
// restarts and multiple gateway replicas. The variadic shape preserves the
// process-local fallback for isolated unit harnesses.
func newGithubdProxy(target string, secret []byte, next http.Handler, log *slog.Logger, auditor *gatewaydAuditor, replayStores ...webhookReplayStore) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	if target == "" {
		log.Warn("githubd proxy disabled (empty target)")
		return next
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Warn("githubd proxy target invalid; /webhooks/github disabled", "target", target, "err", err)
		return next
	}
	if len(secret) == 0 {
		log.Warn("githubd proxy secret unset; /webhooks/github requests will be rejected")
	} else {
		log.Info("githubd proxy armed", "target", u.String())
	}
	var replay webhookReplayStore
	if len(replayStores) > 0 {
		replay = replayStores[0]
	}
	return &githubdProxy{
		target:    u,
		secret:    secret,
		next:      next,
		log:       log,
		transport: &http.Transport{},
		auditor:   auditor,
		replay:    replay,
	}
}

// ServeHTTP routes /webhooks/github to githubd (after HMAC verify),
// otherwise fall through to next.
func (g *githubdProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, githubWebhookPath) {
		g.next.ServeHTTP(w, r)
		return
	}
	if r.URL.Path != githubWebhookPath {
		// /webhooks/github/anything is not our concern today.
		g.next.ServeHTTP(w, r)
		return
	}
	g.handleWebhook(w, r)
}

// handleWebhook reads the body, verifies the X-Hub-Signature-256
// header, and on success reverse-proxies the request verbatim to
// githubd's loopback listener. Any verify failure returns 401.
// Body buffering is required so we can both verify AND forward.
//
// Issue #294: after the HMAC check we consult the shared webhook
// dedupe table. A redelivered X-GitHub-Delivery within the 5-minute
// TTL returns 200 (idempotent — GitHub interprets as success) and
// emits a webhook.replay_rejected audit row. A missing
// X-GitHub-Delivery header returns 400 (a misconfigured client, not
// a replay — GitHub always sets this header).
func (g *githubdProxy) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, api.GitHubWebhookMaxBodyBytes))
	if err != nil {
		g.log.Warn("githubd proxy body read failed", "err", err)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if err := githubd.VerifyPushSignature(body, sig, g.secret); err != nil {
		g.log.Warn("githubd proxy signature verify failed", "err", err)
		http.Error(w, "signature verification failed", http.StatusUnauthorized)
		return
	}
	// Replay check. We require the delivery UUID header
	// (GitHub always sends it; a missing one is a misconfigured
	// client) and consult the shared dedupe helper. The helper is
	// durable store when production wiring is present.
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID == "" {
		g.log.Warn("githubd proxy missing X-GitHub-Delivery header")
		http.Error(w, "missing delivery id", http.StatusBadRequest)
		return
	}
	if err := g.checkReplay(r.Context(), deliveryID); err != nil {
		if !webhookdedupe.IsReplay(err) {
			g.log.Error("githubd replay claim failed", "delivery_id", logsanitize.Field(deliveryID), "err", err)
			http.Error(w, "webhook replay protection unavailable", http.StatusServiceUnavailable)
			return
		}
		g.log.Info("githubd replay rejected", "delivery_id", logsanitize.Field(deliveryID), "err", err)
		if g.auditor != nil {
			g.auditor.Emit(r.Context(), "webhook.replay_rejected", nil, map[string]any{
				"provider":    webhookdedupe.ProviderGitHub,
				"delivery_id": logsanitize.Field(deliveryID),
			})
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	// Hand the original body back to the upstream via a fresh
	// request body reader. We rebuild the upstream URL from the
	// parsed *url.URL rather than concatenating strings — the
	// upstream target is operator-controlled, but using the parsed
	// scheme+host+path keeps linters (gosec) from flagging this
	// as a taint flow we don't actually have.
	upstream := *g.target
	upstream.Path = r.URL.Path
	req2, err := http.NewRequest(http.MethodPost, upstream.String(), bytes.NewReader(body))
	if err != nil {
		g.releaseReplay(r.Context(), deliveryID)
		g.log.Error("githubd proxy build upstream request", "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	req2.Header = r.Header.Clone()
	req2.Host = g.target.Host
	if req2.Header.Get("x-faas-request-id") == "" {
		req2.Header.Set("x-faas-request-id", middleware.NewRequestID())
	}
	resp, err := g.transport.RoundTrip(req2)
	if err != nil {
		g.releaseReplay(r.Context(), deliveryID)
		g.log.Error("githubd proxy upstream error", "err", err)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"githubd_unavailable","status":502,"detail":"webhook upstream not reachable"}`))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// The edge claim is only final after githubd durably accepts the
		// delivery. Release every non-2xx response so a secret-rotation race or
		// transient validation/storage failure does not turn GitHub's retry into
		// a false replay acknowledgement.
		g.releaseReplay(r.Context(), deliveryID)
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// checkReplay claims the delivery in the durable production store. It returns
// nil on a fresh delivery,
// *webhookdedupe.Replay (errors.Is(webhookdedupe.ErrReplay)) on a
// redelivery within the TTL window. The 200 response is emitted
// at the call site; the auditor (if non-nil) emits the audit row.
//
// Production uses the durable webhook_deliveries claim. Tests that omit a
// store retain the legacy process-local helper.
func (g *githubdProxy) checkReplay(ctx context.Context, deliveryID string) error {
	if g.replay != nil {
		now := time.Now().UTC()
		claimed, err := g.replay.ClaimWebhookDelivery(ctx, webhookdedupe.ProviderGitHub, deliveryID,
			now.Add(-webhookdedupe.TTL), now.Add(webhookdedupe.TTL))
		if err != nil {
			return err
		}
		if !claimed {
			return &webhookdedupe.Replay{Provider: webhookdedupe.ProviderGitHub, DeliveryID: deliveryID}
		}
		return nil
	}
	return webhookdedupe.CheckReplay(ctx, webhookdedupe.ProviderGitHub, deliveryID)
}

func (g *githubdProxy) releaseReplay(ctx context.Context, deliveryID string) {
	if g.replay == nil {
		webhookdedupe.ReleaseReplay(ctx, webhookdedupe.ProviderGitHub, deliveryID)
		return
	}
	if err := g.replay.ReleaseWebhookDelivery(ctx, webhookdedupe.ProviderGitHub, deliveryID); err != nil {
		g.log.Warn("githubd replay claim release failed", "delivery_id", logsanitize.Field(deliveryID), "err", err)
	}
}

// loadGithubWebhookSecret reads FAAS_GITHUB_WEBHOOK_SECRET from env
// (the spec §11 + ADR-012 location for the GitHub App webhook secret).
// Empty = secret unset (githubdProxy will reject every webhook).
func loadGithubWebhookSecret(getenv func(string) string) []byte {
	raw := strings.TrimSpace(getenv("FAAS_GITHUB_WEBHOOK_SECRET"))
	if raw == "" {
		// Fall back to the deprecated env name to ease the
		// dev→prod migration; both should agree in production.
		raw = strings.TrimSpace(getenv("FAAS_WEBHOOK_SECRET"))
	}
	if raw == "" {
		return nil
	}
	return []byte(raw)
}

// osGetenv is the default getenv for loadGithubWebhookSecret.
var osGetenv = os.Getenv

// compile-time guard so the httputil import isn't dropped if the
// slim build path removes githubdProxy (an unlikely future refactor).
var _ = httputil.NewSingleHostReverseProxy
