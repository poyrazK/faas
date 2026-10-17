package object

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	if registry != nil && registry.Accounting.GatewaySafety() {
		return errors.New("apid: gateway safety accounting requires object storage billing to be off")
	}
	if registry == nil || registry.Pricing == nil {
		return errors.New("apid: object storage billing requires object-storage pricing")
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
	snapshot, err := st.ObjectUsage(r.Context(), acct.ID, now)
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

func (s *Service) ReconcileObjectInventories(ctx context.Context, observe func(string)) error {
	st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
	if !ok || s.Host.ObjectStorage() == nil {
		return nil
	}
	rows, err := st.DueObjectInventories(ctx, 10)
	if err != nil {
		return err
	}
	for _, b := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		native, err := s.queueNativeObjectInventory(ctx, st, b)
		if err != nil {
			return err
		}
		if native {
			continue
		}
		token := uuid.NewString()
		if err := st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
			if errors.Is(err, state.ErrConflict) {
				continue
			}
			return err
		}
		err = s.ScanObjectInventory(ctx, st, b, token)
		outcome := "success"
		if err != nil {
			outcome = "failed"
			s.Host.Logger().Warn("object storage inventory failed", "bucket_id", b.ID, "backend_id", b.BackendID)
		}
		if observe != nil {
			observe(outcome)
		}
	}
	return nil
}

func (s *Service) ScanObjectInventory(ctx context.Context, st state.ObjectStorageAccountingStore, b state.ObjectBucket, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return err
	}
	bytes, objects, err := CompleteObjectInventory(ctx, backend.Provider, b.PhysicalName, s.inventoryRequestRecorder(b))
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
	if s.Host.Ops() != nil {
		s.Host.Ops().Registry().MustRegister(counter)
	}
	var finalizedPeriod time.Time
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
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
		if err := s.ReconcileObjectInventories(ctx, func(outcome string) { counter.WithLabelValues(outcome).Inc() }); err != nil && ctx.Err() == nil {
			s.Host.Logger().Warn("object storage inventory sweep failed")
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
	return billing.FinalizeObjectStoragePeriods(ctx, runner, runner, pricing, periodStart, now, sink)
}
