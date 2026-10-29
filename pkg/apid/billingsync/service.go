package billingsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is apid's side of reconciliation (ADR-982).
type Host interface {
	Store() state.Store
	// Provider names the active billing provider and its subscription
	// listing; ok=false when none is configured or it cannot list.
	Provider() (name string, lister billing.CustomerSubscriptionLister, ok bool)
	// Account loads the account bound to the provider customer, with the
	// provider's identity overlaid.
	Account(ctx context.Context, provider, customerID string) (state.Account, error)
	// PlanFromProviderID maps a provider plan ID to a plan; "" when none.
	PlanFromProviderID(id string) api.Plan
	// Apply runs a correction through the transitions a verified webhook
	// delivery of the same event takes.
	Apply(ctx context.Context, ev billing.Event, acct state.Account) error
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
}

// Service runs reconciliation passes.
type Service struct {
	Host    Host
	Metrics *Metrics
	// RequestSpacing is the pause before each provider read. Start defaults
	// it to api.BillingReconcileRequestSpacing.
	RequestSpacing time.Duration

	mu sync.Mutex
	// pending holds each difference the last pass saw but did not correct,
	// keyed by account. A difference is corrected when the next pass sees
	// the same one: a delivery still in flight settles it first. Replicas
	// take turns, so a replica's last pass may be long past (pendingAt);
	// then it confirms nothing.
	pending   map[string]string
	pendingAt time.Time
}

// Result summarises one pass.
type Result struct {
	// Skipped means another replica holds the pass.
	Skipped   bool
	Checked   int
	Corrected int
	// Deferred differences are corrected by the next pass if they persist.
	Deferred int
	Failed   int
}

// Start runs a pass every api.BillingReconcileInterval until ctx ends.
func (s *Service) Start(ctx context.Context, log *slog.Logger) {
	if s.RequestSpacing == 0 {
		s.RequestSpacing = api.BillingReconcileRequestSpacing
	}
	go func() {
		ticker := time.NewTicker(api.BillingReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			res, err := s.RunOnce(ctx, log)
			if err != nil && ctx.Err() == nil {
				log.Warn("billing reconcile: pass failed", "err", err)
			} else if res.Corrected > 0 || res.Failed > 0 {
				log.Info("billing reconcile: pass", "checked", res.Checked, "corrected", res.Corrected,
					"deferred", res.Deferred, "failed", res.Failed)
			}
		}
	}()
}

// RunOnce is one pass over every account bound to the active provider. An
// account that fails is retried by the next pass; it does not stop this one.
func (s *Service) RunOnce(ctx context.Context, log *slog.Logger) (Result, error) {
	var res Result
	if s.Host == nil {
		return res, nil
	}
	provider, lister, ok := s.Host.Provider()
	if !ok {
		return res, nil
	}
	identities, ok := s.Host.Store().(state.BillingIdentityLister)
	if !ok {
		return res, nil
	}
	if locker, ok := s.Host.Store().(state.BillingReconcileLocker); ok {
		release, acquired, err := locker.TryAcquireBillingReconcileLock(ctx)
		if err != nil {
			s.Metrics.observePass(passFailed)
			return res, fmt.Errorf("billing reconcile: lock: %w", err)
		}
		if !acquired {
			s.Metrics.observePass(passSkipped)
			res.Skipped = true
			return res, nil
		}
		defer release(context.WithoutCancel(ctx))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	started := time.Now()
	if started.Sub(s.pendingAt) > 2*api.BillingReconcileInterval {
		s.pending = nil
	}
	seen := make(map[string]string)
	after := ""
	for {
		page, err := identities.ListBillingIdentities(ctx, provider, after, api.BillingReconcileBatchSize)
		if err != nil {
			s.Metrics.observePass(passFailed)
			return res, fmt.Errorf("billing reconcile: list identities: %w", err)
		}
		for _, identity := range page {
			after = identity.AccountID
			if err := s.pause(ctx); err != nil {
				s.Metrics.observePass(passFailed)
				return res, err
			}
			res.Checked++
			if err := s.reconcile(ctx, log, provider, lister, identity.CustomerID, seen, &res); err != nil {
				res.Failed++
				log.Warn("billing reconcile: account", "account", identity.AccountID, "err", err)
			}
		}
		if len(page) < api.BillingReconcileBatchSize {
			break
		}
	}
	s.pending, s.pendingAt = seen, started
	s.Metrics.observePass(passCompleted)
	return res, nil
}

func (s *Service) reconcile(ctx context.Context, log *slog.Logger, provider string, lister billing.CustomerSubscriptionLister,
	customerID string, seen map[string]string, res *Result) error {
	acct, err := s.Host.Account(ctx, provider, customerID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load account: %w", err)
	}
	subs, err := lister.CustomerSubscriptions(ctx, customerID)
	if err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}
	if bound := acct.StripeSubscriptionItem; bound != "" && !listed(subs, bound) {
		log.Warn("billing reconcile: bound subscription missing from the provider listing", "account", acct.ID)
	}
	ev, drift := Decide(acct, subs, s.Host.PlanFromProviderID)
	if !drift {
		return nil
	}
	key := ev.Type.Name() + "|" + ev.SubscriptionID + "|" + ev.PlanID
	seen[acct.ID] = key
	if s.pending[acct.ID] != key || res.Corrected >= api.BillingReconcileMaxCorrectionsPerPass {
		res.Deferred++
		return nil
	}
	ev.EventID = "reconcile:" + uuid.NewString()
	if err := s.Host.Apply(ctx, ev, acct); err != nil {
		return fmt.Errorf("apply %s: %w", ev.Type.Name(), err)
	}
	delete(seen, acct.ID)
	res.Corrected++
	s.Metrics.observeCorrection(ev.Type.Name())
	s.Host.Audit(ctx, "billing.subscription_reconciled", acct.ID, map[string]any{
		"provider":        provider,
		"correction":      ev.Type.Name(),
		"subscription_id": ev.SubscriptionID,
		"plan":            ev.PlanID,
		"from_plan":       string(acct.Plan),
		"from_status":     string(acct.Status),
	})
	log.Info("billing reconcile: corrected account", "account", acct.ID, "correction", ev.Type.Name(),
		"from_plan", acct.Plan, "from_status", acct.Status)
	return nil
}

func (s *Service) pause(ctx context.Context) error {
	if s.RequestSpacing <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(s.RequestSpacing)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func listed(subs []billing.Subscription, id string) bool {
	for _, sub := range subs {
		if sub.ID == id {
			return true
		}
	}
	return false
}
