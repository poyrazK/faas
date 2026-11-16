package managedpostgres

import (
	"context"
	"errors"
	"math"
	"time"
)

// EgressCapProvider is an optional Provider capability: a monthly egress
// quota on a provider object, which the provider enforces itself by stopping
// the object's computes (ADR-20261110160000 slice 4). Quotas are bytes.
type EgressCapProvider interface {
	ObserveEgressCap(ctx context.Context, providerResourceID string) (EgressCapObservation, error)
	SetEgressCap(ctx context.Context, providerResourceID string, quotaBytes int64) error
}

// EgressCapObservation is one provider object's egress quota state for the
// provider's current consumption period. Scope names the object the quota
// lives on; databases that share a scope share one counter and quota.
type EgressCapObservation struct {
	Scope       string
	PeriodStart time.Time
	UsedBytes   int64
	// QuotaBytes zero means no quota.
	QuotaBytes int64
}

// applyEgressQuotas sets each provider object's egress quota to the hold
// ceiling times the databases sharing it: a backstop that stops a database
// the sweep failed to hold, at the same egress the sweep holds it at. The
// sweep's own holds stay what stops a database, so a provider without the
// capability, or one measuring another period, is skipped, and a failed write
// fails the subject for this sweep without undoing its holds. A subject with
// no egress allowance keeps whatever quota it has: zero would remove it.
func (e *CapEnforcer) applyEgressQuotas(ctx context.Context, accountID string, databases []Database, check egressCheck, now time.Time, summary *CapSweepSummary) error {
	if check.ceiling <= 0 || e.mode == CapEnforcementOff {
		return nil
	}
	type scope struct {
		resourceID  string
		provider    EgressCapProvider
		observed    EgressCapObservation
		databaseIDs []string
	}
	byScope := map[string]*scope{}
	var order []string
	month := monthStart(now)
	for _, database := range databases {
		if !capEnforceable(database) {
			continue
		}
		backend, err := e.registry.Resolve(database.BackendID, database.BackendFingerprint)
		if err != nil {
			return err
		}
		provider, ok := backend.Provider.(EgressCapProvider)
		if !ok {
			continue
		}
		observed, err := provider.ObserveEgressCap(ctx, database.ProviderResourceID)
		if err != nil {
			summary.EgressQuotasFailed++
			return err
		}
		if gap := observed.PeriodStart.Sub(month); gap > capPeriodTolerance || gap < -capPeriodTolerance {
			// A quota on another period would bind the wrong month's egress.
			return nil
		}
		s, ok := byScope[observed.Scope]
		if !ok {
			s = &scope{resourceID: database.ProviderResourceID, provider: provider, observed: observed}
			byScope[observed.Scope] = s
			order = append(order, observed.Scope)
		}
		s.databaseIDs = append(s.databaseIDs, database.ID)
	}
	var failures []error
	for _, key := range order {
		s := byScope[key]
		target := egressQuotaTarget(check.ceiling, len(s.databaseIDs))
		if s.observed.QuotaBytes == target {
			summary.EgressQuotasUnchanged++
			continue
		}
		attrs := []any{"account_id", accountID, "database_ids", s.databaseIDs, "used_bytes", s.observed.UsedBytes,
			"quota_bytes", s.observed.QuotaBytes, "target_bytes", target, "hold_ceiling_bytes", check.ceiling, "overage_billed", check.billed}
		if e.mode == CapEnforcementShadow {
			summary.EgressQuotasShadowed++
			e.logger.Info("managed postgres egress quota (shadow)", attrs...)
			continue
		}
		if err := s.provider.SetEgressCap(ctx, s.resourceID, target); err != nil {
			summary.EgressQuotasFailed++
			failures = append(failures, err)
			continue
		}
		summary.EgressQuotasApplied++
		e.logger.Info("managed postgres egress quota applied", attrs...)
	}
	return errors.Join(failures...)
}

// egressQuotaTarget is a provider object's egress quota: the per-database hold
// ceiling for each database that shares it, saturating.
func egressQuotaTarget(ceiling int64, databases int) int64 {
	if databases <= 0 || ceiling <= 0 {
		return 0
	}
	if ceiling > math.MaxInt64/int64(databases) {
		return math.MaxInt64
	}
	return ceiling * int64(databases)
}
