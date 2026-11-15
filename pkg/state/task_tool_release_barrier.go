package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolpublication"
	"golang.org/x/sync/semaphore"
)

// TaskToolExecutionFence is private request metadata, never an API response.
type TaskToolExecutionFence struct{ IntentID, OperationID, ClaimToken string }

// TaskToolExecutionBarrierStore holds the journal barrier until the entire
// native mutation handler returns. Stop/exit decisions use the same lock.
type TaskToolExecutionBarrierStore interface {
	AcquireTaskToolExecutionEffect(context.Context, string, string, TaskToolExecutionFence) (func(), error)
}

func validTaskEffect(r api.TaskToolReleaseIntentRecord, key string, f TaskToolExecutionFence) bool {
	e := r.Execution
	return toolpublication.ValidID(f.IntentID) && toolpublication.ValidID(f.OperationID) && toolpublication.ValidID(f.ClaimToken) &&
		r.ID == f.IntentID && r.RequesterKeyID == key && r.Status == "approved" && time.Now().Before(r.ExpiresAt) &&
		e != nil && e.OperationID == f.OperationID && e.FenceVersion == 1 && e.State == "running" &&
		e.Recovery == nil && e.ExecutorExitedAt == nil && e.ClaimSHA256 == toolpublication.Digest([]byte(f.ClaimToken))
}

func (m *MemStore) lockTaskToolEffect(ctx context.Context, account, id string) (func(), error) {
	value, _ := m.taskToolReleaseEffects.LoadOrStore(account+":"+id, semaphore.NewWeighted(1))
	lock := value.(*semaphore.Weighted)
	if err := lock.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { lock.Release(1) }, nil
}
func (m *MemStore) AcquireTaskToolExecutionEffect(ctx context.Context, account, key string, f TaskToolExecutionFence) (func(), error) {
	release, err := m.lockTaskToolEffect(ctx, account, f.IntentID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	r, ok := m.taskToolReleaseIntents[account+":"+f.IntentID]
	valid := ok && validTaskEffect(r, key, f)
	m.mu.Unlock()
	if !valid {
		release()
		return nil, ErrConflict
	}
	return release, nil
}

// Waiting for a journal row must not occupy a pool connection needed by the
// admitted handler. NOWAIT conflicts roll back before a cancellable pause.
func (s *PgStore) lockTaskToolRelease(ctx context.Context, account, id string) (pgx.Tx, []byte, error) {
	for {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, nil, err
		}
		body, err := toolpublicationdb.New().LockTaskToolReleaseIntentNowait(ctx, tx, toolpublicationdb.LockTaskToolReleaseIntentNowaitParams{AccountID: mustPgUUID(account), ID: id})
		if err == nil {
			return tx, body, nil
		}
		_ = tx.Rollback(ctx)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			return nil, nil, mapErr(err)
		}
		timer := time.NewTimer(api.TaskToolExecutionBarrierPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func (s *PgStore) AcquireTaskToolExecutionEffect(ctx context.Context, account, key string, f TaskToolExecutionFence) (func(), error) {
	// Each admitted handler needs another connection for its actual intent write.
	// Reserve at least half of this store's pool; a one-connection pool fails shut.
	if s.pool == nil || s.pool.Pool == nil || s.pool.Pool.Config().MaxConns < 2 || s.taskToolExecutionSlots == nil {
		return nil, ErrConflict
	}
	if err := s.taskToolExecutionSlots.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	tx, body, err := s.lockTaskToolRelease(ctx, account, f.IntentID)
	if err != nil {
		s.taskToolExecutionSlots.Release(1)
		return nil, err
	}
	release := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
		s.taskToolExecutionSlots.Release(1)
	}
	var r api.TaskToolReleaseIntentRecord
	if err := json.Unmarshal(body, &r); err != nil {
		release()
		return nil, err
	}
	if !validTaskEffect(r, key, f) {
		release()
		return nil, ErrConflict
	}
	return release, nil
}
