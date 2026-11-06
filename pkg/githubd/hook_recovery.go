package githubd

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TransportRecoveryStore claims at most three attempts per delivery GUID across
// workers and restarts, and refuses events already accepted by the local inbox.
type TransportRecoveryStore interface {
	ClaimTransportRecovery(context.Context, string, time.Time) (bool, error)
	DelayTransportRecovery(context.Context, string, time.Time) error
	PruneTransportRecoveries(context.Context) error
}

func (s *PGWebhookStore) ClaimTransportRecovery(ctx context.Context, guid string, now time.Time) (bool, error) {
	n, err := sqlc.New().ClaimGitHubTransportRecovery(ctx, s.pool, sqlc.ClaimGitHubTransportRecoveryParams{Guid: guid, AttemptAt: pgtype.Timestamptz{Time: now, Valid: true}})
	return n > 0, err
}
func (s *PGWebhookStore) DelayTransportRecovery(ctx context.Context, guid string, retry time.Time) error {
	q := sqlc.New()
	if err := q.DelayGitHubTransportRecovery(ctx, s.pool, sqlc.DelayGitHubTransportRecoveryParams{Guid: guid, RetryAt: pgtype.Timestamptz{Time: retry, Valid: true}}); err != nil {
		return err
	}
	return q.DelayGitHubTransportRecovery(ctx, s.pool, sqlc.DelayGitHubTransportRecoveryParams{Guid: "", RetryAt: pgtype.Timestamptz{Time: retry, Valid: true}})
}
func (s *PGWebhookStore) PruneTransportRecoveries(ctx context.Context) error {
	return sqlc.New().PruneGitHubTransportRecoveries(ctx, s.pool)
}

func (m *HookDeliveryMonitor) repair(ctx context.Context, deliveries []HookDelivery, now time.Time) error {
	if m.Recovery == nil {
		return nil
	}
	if err := m.Recovery.PruneTransportRecoveries(ctx); err != nil {
		return err
	}
	accepted := make(map[string]bool)
	for _, d := range deliveries {
		if !d.Failed() {
			accepted[d.GUID] = true
		}
	}
	seen := make(map[string]bool)
	attempted := 0
	for _, d := range deliveries {
		if attempted >= 10 {
			break
		}
		if d.GUID == "" || d.ID <= 0 || seen[d.GUID] || accepted[d.GUID] || d.DeliveredAt.Before(now.Add(-m.window())) || d.DeliveredAt.After(now.Add(-2*time.Minute)) {
			continue
		}
		// Authentication/configuration failures need a human repair. Redelivering
		// them automatically would spend retries on a permanently invalid request.
		if d.StatusCode != 0 && d.StatusCode != 408 && d.StatusCode != 429 && d.StatusCode < 500 {
			continue
		}
		switch d.Event {
		case "push", "pull_request", "installation", "installation_repositories", "repository", "installation_target":
		default:
			continue
		}
		seen[d.GUID] = true
		claimed, err := m.Recovery.ClaimTransportRecovery(ctx, d.GUID, now)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		attempted++
		jwt, err := m.MintJWT()
		if err != nil {
			return err
		}
		base := m.BaseURL
		if base == "" {
			base = GitHubAPI
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/hook/deliveries/%d/attempts", base, d.ID), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		client := m.Client
		if client == nil {
			client = NewHTTPClient()
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("githubd: request webhook redelivery: %w", err)
		}
		_ = resp.Body.Close() // no response payload is needed
		if resp.StatusCode == http.StatusAccepted {
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			retry := now.Add(time.Hour)
			if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
				retry = now.Add(time.Duration(min(seconds, 86400)) * time.Second)
			} else if at, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil && at.After(now) {
				retry = at
				if retry.After(now.Add(24 * time.Hour)) {
					retry = now.Add(24 * time.Hour)
				}
			}
			if err := m.Recovery.DelayTransportRecovery(ctx, d.GUID, retry); err != nil {
				return err
			}
		}
		return fmt.Errorf("githubd: request webhook redelivery: GitHub status %d", resp.StatusCode)
	}
	return nil
}
func (m *HookDeliveryMonitor) window() time.Duration {
	if m.Window > 0 {
		return m.Window
	}
	return DefaultHookDeliveryWindow
}
