package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) replayDevBridgeWebhook(w http.ResponseWriter, r *http.Request, account state.Account) {
	bridges, ok := s.devBridgeStore(w)
	if !ok {
		return
	}
	ledger, ok := s.store.(state.DevBridgeWebhookStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("webhook replay receipts unavailable"))
		return
	}
	var input api.ReplayDevBridgeWebhookRequest
	if decodeJSON(r, &input) != nil || len(input.IdempotencyKey) == 0 || len(input.IdempotencyKey) > api.DevBridgeReplayKeyBytes {
		api.WriteProblem(w, devBridgeValidation("invocation_id, request_token and idempotency_key are required"))
		return
	}
	session, original, problem := s.devBridgeWebhookSource(r, account.ID, bridges, input)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	receipt, created, err := ledger.ReserveDevBridgeWebhookReplay(r.Context(), devbridge.WebhookReplay{ID: devbridge.WebhookReplayID(session.ID, input.IdempotencyKey), SessionID: session.ID, AccountID: account.ID, InvocationID: original.ID, IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		api.WriteProblem(w, api.NewProblem(409, "dev_bridge_replay_conflict", "Replay unavailable", "the key identifies another delivery, the session expired, or the replay limit was reached"))
		return
	}
	if created {
		receipt, err = s.finishDevBridgeWebhook(r.Context(), ledger, session, input.RequestToken, receipt, original)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("replay outcome could not be saved; reuse the same idempotency key to inspect the receipt"))
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, receipt)
}

func (s *server) devBridgeWebhookSource(r *http.Request, account string, bridges state.DevBridgeStore, input api.ReplayDevBridgeWebhookRequest) (devbridge.Session, state.Invocation, *api.Problem) {
	var original state.Invocation
	session, err := bridges.DevBridgeByID(r.Context(), account, r.PathValue("id"))
	if err != nil || session.AuthorizeRequest(time.Now(), input.RequestToken, account, session.Scope.EnvironmentID, session.Scope.TargetAppID) != nil {
		return session, original, api.NewProblem(403, "dev_bridge_unauthorized", "Replay denied", "invalid development session")
	}
	original, err = s.store.InvocationByID(r.Context(), input.InvocationID)
	if err != nil || original.AccountID != account || original.AppID != session.Scope.TargetAppID || original.Source != state.InvocationInboundWebhook {
		return session, original, api.NewProblem(404, "not_found", "Not found", "select a provider-verified webhook receipt for the intercepted app")
	}
	return session, original, nil
}

func (s *server) finishDevBridgeWebhook(ctx context.Context, ledger state.DevBridgeWebhookStore, session devbridge.Session, token string, receipt devbridge.WebhookReplay, original state.Invocation) (devbridge.WebhookReplay, error) {
	code, err := s.dispatchDevBridgeWebhook(ctx, session, token, receipt, original)
	receipt.State, receipt.HTTPStatus = "completed", code
	if err != nil {
		receipt.State, receipt.HTTPStatus = "uncertain", 0
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := ledger.FinishDevBridgeWebhookReplay(cleanup, session.Scope.AccountID, receipt.ID, receipt.State, receipt.HTTPStatus); err != nil {
		return receipt, err
	}
	now := time.Now().UTC()
	receipt.CompletedAt = &now
	return receipt, nil
}

func (s *server) getDevBridgeWebhookReplay(w http.ResponseWriter, r *http.Request, account state.Account) {
	if _, ok := s.devBridgeStore(w); !ok {
		return
	}
	ledger, ok := s.store.(state.DevBridgeWebhookStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("webhook replay receipts unavailable"))
		return
	}
	replay, err := ledger.DevBridgeWebhookReplayByID(r.Context(), account.ID, r.PathValue("id"), r.PathValue("replay"))
	if err != nil {
		s.notFound(w, "no such development webhook replay")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, replay)
}

func (s *server) dispatchDevBridgeWebhook(ctx context.Context, session devbridge.Session, token string, receipt devbridge.WebhookReplay, original state.Invocation) (int, error) {
	target, err := url.Parse(s.devBridgeURL)
	if err != nil {
		return 0, err
	}
	ip := net.ParseIP(target.Hostname())
	if target.Scheme != "http" || ip == nil || !ip.IsLoopback() {
		return 0, devbridge.ErrUnauthorized
	}
	path, err := url.ParseRequestURI(original.Path)
	if err != nil {
		return 0, err
	}
	target.Path = "/v1/dev/bridges/" + session.ID + "/traffic" + path.Path
	if path.RawPath != "" {
		target.RawPath = "/v1/dev/bridges/" + session.ID + "/traffic" + path.EscapedPath()
	}
	target.RawQuery = path.RawQuery
	ctx, cancel := context.WithTimeout(ctx, api.DevBridgeWebhookReplayTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, original.Method, target.String(), bytes.NewReader(original.Payload))
	if err != nil {
		return 0, err
	}
	request.Header = devBridgeWebhookReplayHeaders(original, receipt)
	request.Header.Set(devbridge.AccountHeader, session.Scope.AccountID)
	request.Header.Set(devbridge.TokenHeader, token)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, api.DevBridgeReplayResponseBytes))
	return response.StatusCode, err
}

func devBridgeWebhookReplayHeaders(original state.Invocation, receipt devbridge.WebhookReplay) http.Header {
	var source map[string]string
	_ = json.Unmarshal(original.Headers, &source)
	headers := make(http.Header)
	for _, key := range []string{"content-type", "x-gregale-webhook-endpoint-id", "x-gregale-webhook-event-id", "x-gregale-webhook-provider"} {
		if value := source[key]; value != "" {
			headers.Set(key, value)
		}
	}
	headers.Set("X-Faas-Invocation-Source", string(state.InvocationInboundWebhook))
	headers.Set("X-Gregale-Webhook-Development-Replay", "true")
	headers.Set("X-Gregale-Webhook-Replay-ID", receipt.ID)
	headers.Set("X-Gregale-Webhook-Original-Receipt", original.ID)
	return headers
}
