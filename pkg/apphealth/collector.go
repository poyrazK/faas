package apphealth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

func (c *Collector) Sweep(ctx context.Context) (int, error) {
	if c.Store == nil {
		return 0, errors.New("app health collector requires a store")
	}
	checked := 0
	if _, err := c.prune(ctx); err != nil {
		return 0, fmt.Errorf("prune expired app health history: %w", err)
	}
	for range api.AppHealthCollectorBatch {
		if err := ctx.Err(); err != nil {
			return checked, err
		}
		found, err := c.checkNext(ctx)
		if err != nil {
			return checked, err
		}
		if !found {
			break
		}
		checked++
	}
	return checked, nil
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
