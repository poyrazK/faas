package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/alertwire"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"golang.org/x/sync/errgroup"
)

// AlertWorker never connects to a source or mirror. It runs separately from
// Worker, and monitoring continues even while HTTP delivery or keys fail.
type AlertWorker struct {
	Store      *PostgresStore
	Accounts   []string
	Identities func() ([]*age.X25519Identity, error)
	Sender     func(context.Context, alertwire.Attempt) alertwire.Result
	Now        func() time.Time
	Log        *slog.Logger
}

func alertTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t.UTC(), Valid: true} }
func (w *AlertWorker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}
func (w *AlertWorker) accounts() ([]pgtype.UUID, error) {
	if w.Store == nil || len(w.Accounts) == 0 || len(w.Accounts) > api.DataPipelinePilotAccountsMax {
		return nil, ErrUnavailable
	}
	result := make([]pgtype.UUID, 0, len(w.Accounts))
	for _, account := range w.Accounts {
		id, err := uuid.Parse(account)
		if err != nil {
			return nil, ErrInvalid
		}
		result = append(result, pgtype.UUID{Bytes: id, Valid: true})
	}
	return result, nil
}

// CheckTick atomically advances one persistent debounce state and enqueues any
// resulting event. Row locking makes concurrent monitors and restarts harmless.
func (w *AlertWorker) CheckTick(ctx context.Context) (bool, error) {
	accounts, err := w.accounts()
	if err != nil {
		return false, err
	}
	tx, err := w.Store.pool.Begin(ctx)
	if err != nil {
		return false, storeError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := w.now()
	a, err := w.Store.q.PipelineAlertLock(ctx, tx, sqlc.PipelineAlertLockParams{NowAt: alertTime(now), Accounts: accounts})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storeError(err)
	}
	p, err := w.Store.q.PipelineGet(ctx, tx, sqlc.PipelineGetParams{AccountID: uid(a.AccountID), ID: uid(a.PipelineID)})
	if err != nil {
		return false, storeError(err)
	}
	if p.DesiredState == "deleted" || p.State == "deleted" {
		err = w.Store.q.PipelineAlertDisableDeleted(ctx, tx, uid(a.PipelineID))
		if err == nil {
			err = w.Store.q.PipelineAlertCancelPending(ctx, tx, sqlc.PipelineAlertCancelPendingParams{PipelineID: uid(a.PipelineID), Result: "pipeline_deleted"})
		}
	} else {
		err = w.observe(ctx, tx, a, pipelineRow(p), now)
	}
	if err != nil {
		return false, storeError(err)
	}
	if err = w.Store.q.PipelineAlertPrune(ctx, tx, sqlc.PipelineAlertPruneParams{PipelineID: uid(a.PipelineID), HistoryOffset: api.DataPipelineAlertHistoryMax}); err != nil {
		return false, storeError(err)
	}
	return true, storeError(tx.Commit(ctx))
}

func (w *AlertWorker) observe(ctx context.Context, tx pgx.Tx, a sqlc.DataPipelineAlert, p Pipeline, now time.Time) error {
	code, recovered := alertCondition(p, now)
	candidate := ""
	if a.IncidentID == "" && code != "" {
		candidate = code
	}
	if a.IncidentID != "" && code == "" && recovered {
		candidate = "recovered"
	}
	if candidate != a.CandidateCode {
		a.CandidateCode = candidate
		a.CandidateSince = alertTime(now)
	}
	if candidate == "" {
		a.CandidateSince = pgtype.Timestamptz{}
	}
	debounce := api.DataPipelineAlertDebounce
	if candidate == "recovered" {
		debounce = api.DataPipelineAlertRecoveryDebounce
	}
	if candidate != "" && a.CandidateSince.Valid && now.Sub(a.CandidateSince.Time) >= debounce {
		pending, err := w.Store.q.PipelineAlertPendingCount(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if pending < api.DataPipelineAlertPendingMax {
			kind, incident, eventCode := "incident", uuid.NewString(), candidate
			if candidate == "recovered" {
				kind, incident, eventCode = "recovery", a.IncidentID, a.IncidentCode
			}
			err = w.Store.q.PipelineAlertEnqueue(ctx, tx, sqlc.PipelineAlertEnqueueParams{ID: uuid.NewString(), PipelineID: p.ID, AccountID: p.AccountID, ConfigRevision: a.Revision, IncidentID: incident, Kind: kind, Code: eventCode, OccurredAt: alertTime(now)})
			if err != nil {
				return err
			}
			a.IncidentID, a.IncidentCode = incident, eventCode
			if kind == "recovery" {
				a.IncidentID, a.IncidentCode = "", ""
			}
			a.CandidateCode, a.CandidateSince = "", pgtype.Timestamptz{}
		}
	}
	return w.Store.q.PipelineAlertObserve(ctx, tx, sqlc.PipelineAlertObserveParams{PipelineID: p.ID, CandidateCode: a.CandidateCode, CandidateSince: a.CandidateSince, IncidentID: a.IncidentID, IncidentCode: a.IncidentCode, CheckAt: alertTime(now.Add(api.DataPipelineAlertCheckInterval))})
}

func (w *AlertWorker) destination(a sqlc.DataPipelineAlert) (alertDestination, error) {
	if w.Identities == nil {
		return alertDestination{}, ErrUnavailable
	}
	keys, err := w.Identities()
	if err != nil {
		return alertDestination{}, ErrUnavailable
	}
	namespace, raw, err := secretbox.OpenBytesMulti(keys, a.Ciphertext)
	if err != nil || namespace != alertNamespace(uid(a.AccountID), uid(a.PipelineID)) {
		return alertDestination{}, ErrUnavailable
	}
	var d alertDestination
	if json.Unmarshal(raw, &d) != nil || !validAlertDestination(d) {
		return alertDestination{}, ErrUnavailable
	}
	return d, nil
}

// DeliveryTick sends one attempt. Payload and event ID are reconstructed solely
// from immutable, whitelisted ledger fields. No URL, SQL, rows, or raw errors
// are recorded in history. A lost HTTP acknowledgment can repeat an event.
func (w *AlertWorker) DeliveryTick(ctx context.Context) (bool, error) {
	accounts, err := w.accounts()
	if err != nil {
		return false, err
	}
	now, token := w.now(), uuid.NewString()
	d, err := w.Store.q.PipelineAlertClaimDelivery(ctx, w.Store.pool, sqlc.PipelineAlertClaimDeliveryParams{Accounts: accounts, NowAt: alertTime(now), LeaseToken: token, LeaseUntil: alertTime(now.Add(api.DataPipelineAlertLease))})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storeError(err)
	}
	a, err := w.Store.q.PipelineAlertGet(ctx, w.Store.pool, sqlc.PipelineAlertGetParams{AccountID: uid(d.AccountID), PipelineID: uid(d.PipelineID)})
	if err != nil {
		return false, storeError(err)
	}
	if !a.Enabled || a.Revision != d.ConfigRevision {
		return true, nil
	}
	target, err := w.destination(a)
	result := alertwire.Result{}
	reason := "credential_unavailable"
	if err == nil {
		if w.Sender != nil {
			result = w.send(ctx, target, d)
		}
		reason = "network_failure"
	}
	status := "pending"
	switch {
	case result.Accepted:
		status, reason = "delivered", "accepted"
	case result.Redirect:
		status, reason = "dead", "redirect_rejected"
	case result.Terminal:
		status, reason = "dead", "receiver_rejected"
	case int(d.Attempts)+1 >= api.DataPipelineAlertAttemptsMax:
		status, reason = "dead", "attempts_exhausted"
	}
	now = w.now()
	// A receiver can return a nonstandard three-digit HTTP status. Keep the
	// catalog diagnostic bounded without losing its durable retry accounting.
	if result.StatusCode < 0 || result.StatusCode > 599 {
		result.StatusCode = 0
	}
	_, err = w.Store.q.PipelineAlertFinishDelivery(ctx, w.Store.pool, sqlc.PipelineAlertFinishDeliveryParams{ID: uid(d.ID), LeaseToken: token, NowAt: alertTime(now), Status: status, Result: reason, ResponseCode: int32(result.StatusCode), NextAttemptAt: alertTime(now.Add(alertRetry(int(d.Attempts), result.RetryAfter, now)))})
	return true, storeError(err)
}

func (w *AlertWorker) send(ctx context.Context, target alertDestination, d sqlc.DataPipelineAlertDelivery) alertwire.Result {
	return w.Sender(ctx, alertwire.Attempt{ID: uid(d.ID), URL: target.URL, Secret: target.Secret, PipelineID: uid(d.PipelineID), IncidentID: uid(d.IncidentID), Kind: d.Kind, Code: d.Code, OccurredAt: d.OccurredAt.Time, Now: w.now(), Number: int(d.Attempts) + 1})
}

func alertRetry(attempt int, header string, now time.Time) time.Duration {
	delay := min(api.DataPipelineAlertRetryBase*time.Duration(1<<min(attempt, api.DataPipelineAlertAttemptsMax)), api.DataPipelineAlertRetryMax)
	//nolint:gosec // Retry jitter is not a security primitive.
	delay = delay*3/4 + time.Duration(rand.Int64N(int64(delay/2)+1))
	// Only parse bounded Retry-After input. The raw header is never persisted.
	if len(header) <= api.DataPipelineAlertRetryAfterMaxBytes {
		if seconds, err := strconv.ParseInt(strings.TrimSpace(header), 10, 32); err == nil && seconds > 0 {
			delay = max(delay, min(time.Duration(seconds)*time.Second, api.DataPipelineAlertRetryMax))
		} else if at, err := http.ParseTime(header); err == nil {
			delay = max(delay, min(at.Sub(now), api.DataPipelineAlertRetryMax))
		}
	}
	return min(delay, api.DataPipelineAlertRetryMax)
}

func (w *AlertWorker) Run(ctx context.Context) error {
	if _, err := w.accounts(); err != nil {
		return err
	}
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error { return w.loop(ctx, log, true) })
	group.Go(func() error { return w.loop(ctx, log, false) })
	return group.Wait()
}

func (w *AlertWorker) loop(ctx context.Context, log *slog.Logger, monitor bool) error {
	ticker := time.NewTicker(api.DataPipelineAlertWorkerInterval)
	defer ticker.Stop()
	for {
		for range api.DataPipelineAlertBatch {
			var more bool
			var err error
			if monitor {
				more, err = w.CheckTick(ctx)
			} else {
				more, err = w.DeliveryTick(ctx)
			}
			if err != nil {
				log.Warn("data pipeline health worker operation unavailable", "monitor", monitor)
				break
			}
			if !more {
				break
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
