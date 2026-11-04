package product

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A plan can keep a customer after their trial only with a payment method on
// file (ADR-20261022180921). The portal's card pages record whether one is on
// file, and a background step resolves each ended trial once.

// StartTrialResolution resolves ended trials every
// api.ProductControlTrialResolutionInterval.
func (s *Service) StartTrialResolution(ctx context.Context, log *slog.Logger) {
	if _, ok := s.Host.Store().(state.ProductTrialConversionStore); !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.ProductControlTrialResolutionInterval)
		defer ticker.Stop()
		for {
			if err := s.ResolveTrialsOnce(ctx); err != nil && ctx.Err() == nil {
				log.Warn("product trial resolution failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// ResolveTrialsOnce resolves at most a batch of ended trials and audits each.
// It does nothing while Product Control is disabled.
func (s *Service) ResolveTrialsOnce(ctx context.Context) error {
	store, ok := s.Host.Store().(state.ProductTrialConversionStore)
	if !ok || !s.Enabled {
		return nil
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	resolved, err := store.ResolveProductTrials(ctx, now)
	if err != nil {
		return fmt.Errorf("resolve product trials: %w", err)
	}
	for _, r := range resolved {
		fields := map[string]any{"app_id": r.AppID, "tenant_id": r.TenantID, "plan": r.Plan, "trial_ended_at": r.TrialEndsAt,
			"requires_payment_method": r.RequiresPaymentMethod}
		if r.FallbackPlan == "" {
			s.Host.EmitAudit(ctx, "product.customer.trial_converted", &r.AccountID, fields)
			continue
		}
		fields["fallback_plan"] = r.FallbackPlan
		s.Host.EmitAudit(ctx, "product.customer.trial_fell_back", &r.AccountID, fields)
	}
	return nil
}

// recordPaymentMethod records whether the workspace has a payment method on
// file. A workspace that is not a customer of the app has nothing to record.
func (s *Service) recordPaymentMethod(ctx context.Context, acct state.Account, app state.App, tenant string, onFile bool) error {
	store, ok := s.Host.Store().(state.ProductTrialConversionStore)
	if !ok {
		return nil
	}
	if _, err := store.SetProductCustomerPaymentMethod(ctx, acct.ID, app.ID, tenant, onFile); err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("record product payment method: %w", err)
	}
	return nil
}
