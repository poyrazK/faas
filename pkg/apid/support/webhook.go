package support

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

type Webhook struct {
	endpoint, secret string
	client           *http.Client
}

func NewWebhook(endpoint, secret string) (*Webhook, error) {
	if endpoint == "" && secret == "" {
		return nil, nil
	}
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(secret) < 32 {
		return nil, fmt.Errorf("support webhook requires an HTTPS URL and a secret of at least 32 bytes")
	}
	return &Webhook{endpoint: endpoint, secret: secret, client: &http.Client{Timeout: api.SupportDispatchTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (w *Webhook) send(ctx context.Context, event, ticket, kind string) error {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := raw(map[string]any{"id": event, "ticket_id": ticket, "kind": kind, "version": 1})
	mac := hmac.New(sha256.New, []byte(w.secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, w.endpoint, bytes.NewReader(body))
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Gregale-Event-ID", event)
	r.Header.Set("X-Gregale-Timestamp", timestamp)
	r.Header.Set("X-Gregale-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
	res, e := w.client.Do(r)
	if e != nil {
		return fmt.Errorf("support webhook request failed")
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, api.SupportRequestMaxBytes))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("support webhook returned HTTP %d", res.StatusCode)
	}
	return nil
}
func (s *Service) dispatch(ctx context.Context) error {
	queries := q.New()
	now := time.Now().UTC()
	lease := newID()
	row, e := queries.OutboxClaim(ctx, s.Pool, q.OutboxClaimParams{AvailableAt: ts(now), LeaseID: uid(lease), LeaseUntil: ts(now.Add(api.SupportWebhookLease))})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if row.Attempts > api.SupportWebhookMaxAttempts {
		return queries.OutboxFinish(ctx, s.Pool, q.OutboxFinishParams{ID: row.ID, LeaseID: uid(lease), State: "failed", AvailableAt: ts(now)})
	}
	state := "accepted"
	kind, e := queries.OutboxEvent(ctx, s.Pool, row.ID)
	if e == nil {
		e = s.Webhook.send(ctx, id(row.ID), id(row.TicketID), kind)
	}
	available := now
	if e != nil {
		state = "queued"
		available = now.Add(time.Duration(row.Attempts) * api.SupportWorkerInterval)
		if row.Attempts >= api.SupportWebhookMaxAttempts {
			state = "failed"
		}
	}
	return queries.OutboxFinish(ctx, s.Pool, q.OutboxFinishParams{ID: row.ID, LeaseID: uid(lease), State: state, AvailableAt: ts(available)})
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(api.SupportWorkerInterval)
	defer ticker.Stop()
	for {
		if e := s.Cleanup(ctx); e != nil && ctx.Err() == nil {
			slog.Error("support preview maintenance failed")
		}
		if s.Webhook != nil {
			for range api.SupportWorkerBatch {
				if ctx.Err() != nil {
					return
				}
				if e := s.dispatch(ctx); e != nil {
					slog.Error("support webhook maintenance failed")
					break
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
