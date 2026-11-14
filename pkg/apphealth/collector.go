package apphealth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

type CollectorStore interface {
	EvidenceStore
	state.AppHealthHistoryStore
	AppByID(context.Context, string) (state.App, error)
	AccountByID(context.Context, string) (state.Account, error)
}

// Collector records observations independently of customer reads. Each app's
// durable lease fences expired workers. Collection has no lifecycle methods.
type Collector struct {
	Store   CollectorStore
	Client  *promql.Client
	Logger  *slog.Logger
	Observe func(string, time.Duration)
}

// Sweep collects due apps with api.AppHealthCollectorWorkers concurrent
// workers, each claiming up to api.AppHealthCollectorBatch apps. One
// sequential worker managed about seven apps a second, so past roughly 500
// quiet apps every app's evidence went stale and rollout watching saw
// expired health (bug hunt O35). Claims are leased and skip locked rows, so
// workers never collect the same app at once.
//
// A claim scans the apps for the next one due, about 4 ms of control-plane
// database time. Most sweeps find nothing due, so the sweep probes with one
// claim before starting the other workers, and the first claim that comes
// back empty stops them all: four workers polling every second were ~70% of
// the claims and the largest statement cost on the control plane (prod
// 2026-11-10: 5.6 claims/s, ~4 of them empty).
func (c *Collector) Sweep(ctx context.Context) (int, error) {
	if c.Store == nil {
		return 0, errors.New("app health collector requires a store")
	}
	if _, err := c.prune(ctx); err != nil {
		return 0, fmt.Errorf("prune expired app health history: %w", err)
	}
	found, err := c.checkNext(ctx)
	if err != nil || !found {
		return 0, err
	}
	var (
		wg       sync.WaitGroup
		checked  atomic.Int64
		drained  atomic.Bool
		errOnce  sync.Once
		firstErr error
	)
	checked.Store(1)
	for worker := range max(api.AppHealthCollectorWorkers, 1) {
		batch := api.AppHealthCollectorBatch
		if worker == 0 {
			batch-- // the probe was this worker's first claim
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range batch {
				if drained.Load() {
					return
				}
				if err := ctx.Err(); err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
				found, err := c.checkNext(ctx)
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
				if !found {
					drained.Store(true)
					return
				}
				checked.Add(1)
			}
		}()
	}
	wg.Wait()
	return int(checked.Load()), firstErr
}

func (c *Collector) prune(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, api.AppHealthCollectionTimeout)
	defer cancel()
	return c.Store.PruneExpiredAppHealthHistory(ctx, time.Now().UTC())
}

func (c *Collector) checkNext(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, api.AppHealthCollectionTimeout)
	defer cancel()
	started := time.Now().UTC()
	claim, err := c.Store.ClaimAppHealth(ctx, uuid.NewString(), started)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim next app health collection: %w", err)
	}
	outcome := "error"
	defer func() {
		if c.Observe != nil {
			c.Observe(outcome, time.Since(started))
		}
	}()
	if err := c.collectClaim(ctx, claim); err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return true, ctx.Err()
		}
		if c.Logger != nil {
			c.Logger.Warn("app health collection incomplete", "app_id", claim.AppID, "error", err)
		}
		// The lease expires without publishing a success. Other apps continue;
		// the next successful observation records any expired-evidence gap.
		return true, nil
	}
	outcome = "recorded"
	return true, nil
}

func (c *Collector) collectClaim(ctx context.Context, claim state.AppHealthClaim) error {
	app, err := c.Store.AppByID(ctx, claim.AppID)
	if err != nil {
		return fmt.Errorf("read health collection app: %w", err)
	}
	account, err := c.Store.AccountByID(ctx, claim.AccountID)
	if err != nil {
		return fmt.Errorf("read health collection account: %w", err)
	}
	assessment := Collect(ctx, c.Store, c.Client, app, account.Plan.PerAppMetricsAllowed())
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.Store.FinishAppHealth(ctx, claim, assessment, time.Now().UTC()); err != nil {
		return fmt.Errorf("record health collection: %w", err)
	}
	return nil
}

func (c *Collector) Run(ctx context.Context) error {
	for {
		if _, err := c.Sweep(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if c.Logger != nil {
				c.Logger.Error("app health collector sweep failed", "error", err)
			}
		}
		timer := time.NewTimer(api.AppHealthCollectorIdleInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
