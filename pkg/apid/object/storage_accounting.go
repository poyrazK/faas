package object

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func ValidateObjectStorageBillingSetup(provider billing.Provider, registry *objectstorage.Registry) error {
	sink, ok := provider.(billing.ObjectStorageLineItemSink)
	if !ok {
		return nil
	}
	_, enabled := sink.ObjectStorageLineItemPolicy()
	if !enabled {
		return nil
	}
	if registry == nil || registry.Pricing == nil {
		return errors.New("apid: object storage billing requires object-storage pricing")
	}
	// Gateway safety bills Gregale's own storage meter, so only a
	// storage-only rate card can be charged (ADR-20261024132844).
	if registry.Accounting.GatewaySafety() && !registry.Pricing.StorageOnly() {
		return errors.New("apid: gateway safety accounting bills storage only; set request and egress prices to zero")
	}
	if !registry.Pricing.Valid() {
		return errors.New("apid: object storage billing requires a valid object-storage rate card")
	}
	// Every Gregale charge is in the billing currency (ADR-1430).
	if registry.Pricing.Currency != api.BillingCurrency {
		return fmt.Errorf("apid: object storage billing requires %s pricing, got %q", api.BillingCurrency, registry.Pricing.Currency)
	}
	return nil
}

func (s *Service) AdmitObjectURL(ctx context.Context, b state.ObjectBucket, r objectstorage.SignRequest) error {
	st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
	if !ok || s.Host.ObjectStorage() == nil {
		return state.ErrObjectUsageStale
	}
	size := int64(0)
	if r.SizeBytes != nil {
		size = *r.SizeBytes
	}
	return st.AdmitObjectURL(ctx, b.AccountID, b.ID, r.Key, size, r.Method == http.MethodPut, s.Host.ObjectStorage().Accounting)
}

func (s *Service) admitObjectMultipartPartURL(ctx context.Context, b state.ObjectBucket, key string) error {
	st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
	if !ok || s.Host.ObjectStorage() == nil {
		return state.ErrObjectUsageStale
	}
	// The complete object size is reserved at session creation. Every part URL
	// still consumes the monthly authorization budget and rechecks freshness,
	// provider spend, request, and egress ceilings before a capability escapes.
	return st.AdmitObjectURL(ctx, b.AccountID, b.ID, key, 0, false, s.Host.ObjectStorage().Accounting)
}

func (s *Service) GetObjectStorageUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
	if !ok || s.Host.ObjectStorage() == nil {
		BucketProblem(w, state.ErrObjectUsageStale)
		return
	}
	now := time.Now().UTC()
	snapshot, err := st.ObjectUsage(r.Context(), acct.ResourceOwnerID(), now)
	if err != nil {
		BucketProblem(w, err)
		return
	}
	usage := state.SummarizeObjectUsage(snapshot, s.Host.ObjectStorage().Accounting, now)
	charges, err := s.Host.ObjectStorage().ChargeForUsage(usage, api.MustLimitsFor(acct.Plan).ObjectStorage.Allowance())
	if err != nil {
		BucketProblem(w, err)
		return
	}
	httpkit.WriteJSON(w, 200, s.ObjectStorageUsageResponse(usage, charges, acct.Plan))
}

// ObjectStorageUsageResponse reports the account's effective policy: the
// operator safety policy tightened by its plan's ceilings (ADR-1320).
func (s *Service) ObjectStorageUsageResponse(usage api.ObjectStorageUsage, charges *api.ObjectStorageCharge, plan api.Plan) api.ObjectStorageUsageResponse {
	out := api.ObjectStorageUsageResponse{
		Usage:       usage,
		Policy:      api.MustLimitsFor(plan).ObjectStorage.Policy(s.Host.ObjectStorage().Accounting),
		Charges:     charges,
		BillingMode: "off",
	}
	if sink, ok := s.Host.BillingProvider().(billing.ObjectStorageLineItemSink); ok {
		if policy, enabled := sink.ObjectStorageLineItemPolicy(); enabled {
			out.BillingMode = string(policy.Mode)
			effectiveFrom := policy.EffectiveFrom
			out.BillingFrom = &effectiveFrom
		}
	}
	return out
}

func (s *Service) RecordObjectStorageUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, problem := s.Host.AdminAllows(acct); !allowed {
		api.WriteProblem(w, problem)
		return
	}
	var report api.ObjectStorageUsageReport
	if !decodeBucketRequest(w, r, &report) {
		return
	}
	if _, err := uuid.Parse(report.AccountID); err != nil {
		BucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if _, err := s.Host.ObjectStorage().Resolve(report.BackendID, report.BackendFingerprint); err != nil {
		BucketProblem(w, err)
		return
	}
	st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
	if !ok {
		BucketProblem(w, state.ErrObjectUsageStale)
		return
	}
	if err := st.RecordObjectUsageReport(r.Context(), report); err != nil {
		BucketProblem(w, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "object_storage.usage_reported", &report.AccountID, map[string]any{"actor_id": acct.ID, "backend_id": report.BackendID, "observed_at": report.ObservedAt})
	w.WriteHeader(http.StatusNoContent)
}

// ReconcileObjectInventories refreshes due bucket inventories. Periodic
// inventory listings are platform maintenance: they never count toward the
// account's request ceiling (ADR-20261023223000).
func (s *Service) ReconcileObjectInventories(ctx context.Context, observe func(string)) error {
	return s.reconcileObjectInventories(ctx, observe, nil)
}

// reconcileObjectInventories drains due inventories in batches of
// ObjectStorageInventorySweepBatch, scanning up to
// ObjectStorageInventorySweepConcurrency buckets at once, and starts no new
// batch once ObjectStorageInventorySweepBudget has passed. A bucket returned
// twice in one sweep did not advance its claim, so the sweep stops there
// rather than spin on it. page, when set, counts each provider listing page.
func (s *Service) reconcileObjectInventories(ctx context.Context, observe func(string), page func()) error {
	st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
	if !ok || s.Host.ObjectStorage() == nil {
		return nil
	}
	var observeMu sync.Mutex
	report := func(outcome string) {
		if observe != nil {
			observeMu.Lock()
			defer observeMu.Unlock()
			observe(outcome)
		}
	}
	deadline := time.Now().Add(api.ObjectStorageInventorySweepBudget)
	seen := map[string]bool{}
	for time.Now().Before(deadline) {
		rows, err := st.DueObjectInventories(ctx, api.ObjectStorageInventorySweepBatch)
		if err != nil {
			return err
		}
		batch := make([]state.ObjectBucket, 0, len(rows))
		for _, b := range rows {
			if !seen[b.ID] {
				seen[b.ID] = true
				batch = append(batch, b)
			}
		}
		if len(batch) == 0 {
			return nil
		}
		if err := s.reconcileObjectInventoryBatch(ctx, st, batch, report, page); err != nil {
			return err
		}
		if len(rows) < api.ObjectStorageInventorySweepBatch || len(batch) < len(rows) {
			return nil
		}
	}
	return nil
}

// reconcileObjectInventoryBatch scans one batch with bounded concurrency and
// returns the first store or configuration error after the batch finishes.
func (s *Service) reconcileObjectInventoryBatch(ctx context.Context, st state.ObjectStorageAccountingStore, batch []state.ObjectBucket, report func(string), page func()) error {
	work := make(chan state.ObjectBucket)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var first error
	for range min(api.ObjectStorageInventorySweepConcurrency, len(batch)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range work {
				if err := s.reconcileObjectInventory(ctx, st, b, report, page); err != nil {
					errMu.Lock()
					if first == nil {
						first = err
					}
					errMu.Unlock()
				}
			}
		}()
	}
	for _, b := range batch {
		if ctx.Err() != nil {
			break
		}
		work <- b
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	return first
}

func (s *Service) reconcileObjectInventory(ctx context.Context, st state.ObjectStorageAccountingStore, b state.ObjectBucket, report func(string), page func()) error {
	native, err := s.queueNativeObjectInventory(ctx, st, b)
	if err != nil || native {
		return err
	}
	token := uuid.NewString()
	if err := st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
		if errors.Is(err, state.ErrConflict) {
			return nil
		}
		return err
	}
	outcome := "success"
	if err := s.scanObjectInventory(ctx, st, b, token, page); err != nil {
		outcome = "failed"
		s.Host.Logger().Warn("object storage inventory failed", "bucket_id", b.ID, "backend_id", b.BackendID)
	}
	report(outcome)
	return nil
}

// ScanObjectInventory lists one claimed bucket and publishes its inventory.
// The listing is maintenance and is not charged to the account's requests.
func (s *Service) ScanObjectInventory(ctx context.Context, st state.ObjectStorageAccountingStore, b state.ObjectBucket, token string) error {
	return s.scanObjectInventory(ctx, st, b, token, nil)
}

func (s *Service) scanObjectInventory(ctx context.Context, st state.ObjectStorageAccountingStore, b state.ObjectBucket, token string, page func()) error {
	ctx, cancel := context.WithTimeout(ctx, api.ObjectCapacityInventoryTimeout)
	defer cancel()
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return err
	}
	var record func(context.Context) error
	if page != nil {
		record = func(context.Context) error { page(); return nil }
	}
	bytes, objects, counters, err := objectstorage.CompleteOwnedUsageCounters(ctx, backend.Provider, b.PhysicalName, record)
	if !counters {
		bytes, objects, err = CompleteObjectInventory(ctx, backend.Provider, b.PhysicalName, record)
	}
	if err != nil {
		return err
	}
	return st.FinishObjectInventory(ctx, b.ID, token, bytes, objects)
}

func (s *Service) inventoryRequestRecorder(b state.ObjectBucket) func(context.Context) error {
	return func(ctx context.Context) error {
		metrics, ok := s.Host.Store().(state.ObjectStorageProviderUsageStore)
		if !ok {
			if s.Host.ObjectStorage().Accounting.GatewaySafety() {
				return state.ErrObjectUsageStale
			}
			return nil
		}
		return metrics.RecordObjectStorageProviderRequest(ctx, b.ID, time.Now().UTC())
	}
}

// Customer dispatch reserves gateway request usage atomically. Recovery uses
// inventoryRequestRecorder instead so ceilings cannot strand accepted work.
func (s *Service) CustomerObjectRequestRecorder(b state.ObjectBucket) func(context.Context) error {
	if !s.Host.ObjectStorage().Accounting.GatewaySafety() {
		metrics, _ := s.Host.Store().(state.ObjectStorageProviderUsageStore)
		return objectstorage.VersioningRequestRecorder(metrics, b.ID)
	}
	return func(ctx context.Context) error {
		metrics, ok := s.Host.Store().(state.ObjectStorageGatewayRequestStore)
		if !ok {
			return state.ErrObjectUsageStale
		}
		return metrics.ReserveObjectStorageGatewayRequest(ctx, b.ID, time.Now().UTC(), s.Host.ObjectStorage().Accounting)
	}
}

func (s *Service) RunObjectStorageAccounting(ctx context.Context) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_storage_inventory_scans_total", Help: "Complete or failed object storage inventory scans."}, []string{"outcome"})
	pages := prometheus.NewCounter(prometheus.CounterOpts{Name: "faas_object_storage_inventory_provider_requests_total", Help: "Provider listing requests made by periodic inventory scans; platform maintenance, not charged to accounts."})
	accruals := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_storage_gateway_storage_accruals_total", Help: "Gateway-metered stored byte-hour accruals (ADR-20261024132844)."}, []string{"outcome"})
	if s.Host.Ops() != nil {
		s.Host.Ops().Registry().MustRegister(counter, pages, accruals)
	}
	var finalizedPeriod, estimatedAt time.Time
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if now := time.Now().UTC(); now.Sub(estimatedAt) >= api.FinancialObjectStorageEstimateInterval {
			if err := s.RecordObjectStorageEstimates(ctx, now); err != nil && ctx.Err() == nil {
				s.Host.Logger().Warn("object storage budget estimates failed", "err", err)
			}
			estimatedAt = now
		}
		if s.Host.ObjectStorage() != nil {
			st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
			if ok {
				importCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				err := s.Host.ObjectStorage().ImportUsageReports(importCtx, func(r api.ObjectStorageUsageReport) error { return st.RecordObjectUsageReport(importCtx, r) })
				cancel()
				if err != nil && ctx.Err() == nil {
					s.Host.Logger().Warn("object storage provider usage import failed")
				}
			}
		}
		if err := s.reconcileObjectInventories(ctx, func(outcome string) { counter.WithLabelValues(outcome).Inc() }, pages.Inc); err != nil && ctx.Err() == nil {
			s.Host.Logger().Warn("object storage inventory sweep failed")
		}
		if outcome := s.AccrueGatewayStorage(ctx); outcome != "" {
			accruals.WithLabelValues(outcome).Inc()
		}
		if s.Host.ObjectStorage() != nil && s.Host.ObjectStorage().Pricing != nil {
			now := time.Now().UTC()
			period := state.ObjectStoragePeriod(now).AddDate(0, -1, 0)
			if !period.Equal(finalizedPeriod) {
				if records, err := s.finalizeObjectStorageBilling(ctx, *s.Host.ObjectStorage().Pricing, period, now); err != nil {
					if ctx.Err() == nil {
						s.Host.Logger().Warn("object storage billing finalization failed", "period", period.Format("2006-01"), "err", err)
					}
				} else {
					finalizedPeriod = period
					s.Host.Logger().Info("object storage billing period finalized", "period", period.Format("2006-01"), "accounts", len(records))
				}
			}
		}
		ticker.Reset(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.InventoryWake:
		}
	}
}

// AccrueGatewayStorage meters stored bytes under gateway-safety accounting,
// where no provider usage report exists (ADR-20261024132844). It runs after
// the inventory sweep and before month close, and returns "success",
// "failed", or "" when it does not apply.
func (s *Service) AccrueGatewayStorage(ctx context.Context) string {
	registry := s.Host.ObjectStorage()
	if registry == nil || !registry.Accounting.GatewaySafety() {
		return ""
	}
	st, ok := s.Host.Store().(state.ObjectStorageGatewayStorageStore)
	if !ok {
		return ""
	}
	accrueCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := st.AccrueObjectStorageGatewayStorage(accrueCtx); err != nil {
		if ctx.Err() == nil {
			s.Host.Logger().Warn("object storage gateway storage accrual failed", "err", err)
		}
		return "failed"
	}
	return "success"
}

// finalizeObjectStorageBilling is the daemon-side month-close boundary. The
// generic accounting store owns durable idempotency; the optional sink is the
// only place that knows how to turn a finalized record into a provider invoice
// line item. A nil sink is valid during the opt-in preview and still records
// the immutable internal ledger.
func (s *Service) finalizeObjectStorageBilling(ctx context.Context, pricing api.ObjectStoragePricing, periodStart, now time.Time) ([]state.ObjectStorageBillingRecord, error) {
	runner, ok := s.Host.Store().(interface {
		state.ObjectStorageBillingStore
		billing.ObjectStorageAccountSource
	})
	if !ok {
		return nil, errors.New("object storage billing store is unavailable")
	}
	var sink billing.ObjectStorageLineItemSink
	if candidate, ok := s.Host.BillingProvider().(billing.ObjectStorageLineItemSink); ok {
		sink = candidate
	}
	return billing.FinalizeObjectStoragePeriods(ctx, storageBillingAccounts{store: runner}, runner, pricing, periodStart, now, sink)
}
