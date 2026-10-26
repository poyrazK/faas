// Package logerasure deletes an account's archived runtime logs before the
// grace sweep deletes the account (DPA §11). The archive keys objects by
// instance alone (faas-logs/{instance}/...), so the account's rows never
// reached them: the sweep snapshots the instances whose logs go with the
// account, then deletes each instance's objects a batch per tick.
package logerasure

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrPending keeps the account row through another grace tick while archived
// logs remain.
var ErrPending = errors.New("log archive: account logs are not erased yet")

// DefaultBatch is how many instances one grace tick erases. Each costs one
// list call plus a delete per archived day.
const DefaultBatch = 200

// listPageKeys is the list page size; an instance rarely spans more than a
// few archived days.
const listPageKeys = 1000

// Objects is the slice of logarchive.S3Client the erasure needs.
type Objects interface {
	ListObjectKeys(ctx context.Context, prefix, marker string, maxKeys int) ([]string, string, error)
	DeleteObject(ctx context.Context, key string) error
}

// AccountErasure is the grace sweep's archived-log step. A nil Objects means
// this apid has no archive bucket configured, so there is nothing it can
// reach to delete.
type AccountErasure struct {
	Store   state.LogArchiveErasureStore
	Objects Objects
	// Batch caps the instances erased per tick; zero means DefaultBatch.
	Batch int
	// Audit records the completed erasure; the sweep is the actor.
	Audit func(ctx context.Context, kind string, accountID *string, data map[string]any)
	Log   *slog.Logger
}

// BeforeAccountDelete matches grace.Params.BeforeAccountDelete. It returns
// ErrPending until every snapshotted instance's archive prefix is empty.
func (e AccountErasure) BeforeAccountDelete(ctx context.Context, acct state.Account) error {
	if e.Store == nil || e.Objects == nil {
		return nil
	}
	if acct.DeletionRequestedAt == nil {
		return fmt.Errorf("log archive: account %s has no deletion request", acct.ID)
	}
	if err := e.Store.BeginLogArchiveErasure(ctx, acct.ID, *acct.DeletionRequestedAt); err != nil {
		return fmt.Errorf("log archive: begin erasure: %w", err)
	}
	batch := e.Batch
	if batch <= 0 {
		batch = DefaultBatch
	}
	pending, err := e.Store.PendingLogArchiveErasure(ctx, acct.ID, batch)
	if err != nil {
		return fmt.Errorf("log archive: read erasure: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	var objects int
	for _, instance := range pending {
		deleted, err := e.eraseInstance(ctx, instance)
		objects += deleted
		if err != nil {
			e.log().Warn("log archive erasure: instance", "account", acct.ID, "instance", instance, "err", err)
			return fmt.Errorf("%w: %w", ErrPending, err)
		}
		if err := e.Store.MarkLogArchiveErased(ctx, acct.ID, instance); err != nil {
			return fmt.Errorf("log archive: mark erased: %w", err)
		}
	}
	more, err := e.Store.PendingLogArchiveErasure(ctx, acct.ID, 1)
	if err != nil {
		return fmt.Errorf("log archive: read erasure: %w", err)
	}
	if len(more) > 0 {
		return fmt.Errorf("%w: erased %d instances this tick, more remain", ErrPending, len(pending))
	}
	e.log().Info("log archive erasure: complete", "account", acct.ID, "objects_deleted_last_tick", objects)
	if e.Audit != nil {
		accountID := acct.ID
		e.Audit(ctx, "log_archive.erased", &accountID, map[string]any{
			"actor": "grace-sweep", "erased_at": time.Now().UTC().Format(time.RFC3339),
		})
	}
	return nil
}

// eraseInstance deletes every archived object under the instance's prefix and
// reports how many it deleted.
func (e AccountErasure) eraseInstance(ctx context.Context, instance string) (int, error) {
	prefix := "faas-logs/" + instance + "/"
	deleted, marker := 0, ""
	for {
		keys, next, err := e.Objects.ListObjectKeys(ctx, prefix, marker, listPageKeys)
		if err != nil {
			return deleted, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, key := range keys {
			if err := e.Objects.DeleteObject(ctx, key); err != nil {
				return deleted, fmt.Errorf("delete %s: %w", key, err)
			}
			deleted++
		}
		if next == "" {
			return deleted, nil
		}
		marker = next
	}
}

func (e AccountErasure) log() *slog.Logger {
	if e.Log == nil {
		return slog.Default()
	}
	return e.Log
}
