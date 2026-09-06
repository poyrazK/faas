package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

func (s *server) admitObjectURL(ctx context.Context, b state.ObjectBucket, r objectstorage.SignRequest) error {
	st, ok := s.store.(state.ObjectStorageAccountingStore)
	if !ok || s.objectStorage == nil {
		return state.ErrObjectUsageStale
	}
	size := int64(0)
	if r.SizeBytes != nil {
		size = *r.SizeBytes
	}
	return st.AdmitObjectURL(ctx, b.AccountID, b.ID, r.Key, size, r.Method == http.MethodPut, s.objectStorage.Accounting)
}

func (s *server) admitObjectMultipartPartURL(ctx context.Context, b state.ObjectBucket, key string) error {
	st, ok := s.store.(state.ObjectStorageAccountingStore)
	if !ok || s.objectStorage == nil {
		return state.ErrObjectUsageStale
	}
	// The complete object size is reserved at session creation. Every part URL
	// still consumes the monthly authorization budget and rechecks freshness,
	// provider spend, request, and egress ceilings before a capability escapes.
	return st.AdmitObjectURL(ctx, b.AccountID, b.ID, key, 0, false, s.objectStorage.Accounting)
}

func (s *server) getObjectStorageUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	st, ok := s.store.(state.ObjectStorageAccountingStore)
	if !ok || s.objectStorage == nil {
		bucketProblem(w, state.ErrObjectUsageStale)
		return
	}
	now := time.Now().UTC()
	snapshot, err := st.ObjectUsage(r.Context(), acct.ID, now)
	if err != nil {
		bucketProblem(w, err)
		return
	}
	writeJSON(w, 200, api.ObjectStorageUsageResponse{Usage: state.SummarizeObjectUsage(snapshot, s.objectStorage.Accounting, now), Policy: s.objectStorage.Accounting})
}

func (s *server) recordObjectStorageUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, problem := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, problem)
		return
	}
	var report api.ObjectStorageUsageReport
	if !decodeBucketRequest(w, r, &report) {
		return
	}
	if _, err := uuid.Parse(report.AccountID); err != nil {
		bucketProblem(w, objectstorage.ErrInvalid)
		return
	}
	if _, err := s.objectStorage.Resolve(report.BackendID, report.BackendFingerprint); err != nil {
		bucketProblem(w, err)
		return
	}
	st, ok := s.store.(state.ObjectStorageAccountingStore)
	if !ok {
		bucketProblem(w, state.ErrObjectUsageStale)
		return
	}
	if err := st.RecordObjectUsageReport(r.Context(), report); err != nil {
		bucketProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "object_storage.usage_reported", &report.AccountID, map[string]any{"actor_id": acct.ID, "backend_id": report.BackendID, "observed_at": report.ObservedAt})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) reconcileObjectInventories(ctx context.Context, observe func(string)) error {
	st, ok := s.store.(state.ObjectStorageAccountingStore)
	if !ok || s.objectStorage == nil {
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
		token := uuid.NewString()
		if err := st.ClaimObjectInventory(ctx, b.ID, token); err != nil {
			if errors.Is(err, state.ErrConflict) {
				continue
			}
			return err
		}
		err := s.scanObjectInventory(ctx, st, b, token)
		outcome := "success"
		if err != nil {
			outcome = "failed"
			s.log.Warn("object storage inventory failed", "bucket_id", b.ID, "backend_id", b.BackendID)
		}
		if observe != nil {
			observe(outcome)
		}
	}
	return nil
}

func (s *server) scanObjectInventory(ctx context.Context, st state.ObjectStorageAccountingStore, b state.ObjectBucket, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return err
	}
	var bytes, objects int64
	cursor := ""
	seen := map[string]bool{}
	for range api.ObjectStorageInventoryMaxPages {
		page, err := backend.Provider.ListObjects(ctx, b.PhysicalName, "", cursor, 1000)
		if err != nil {
			return err
		}
		if len(page.Items) > 1000 {
			return objectstorage.ErrInvalid
		}
		for _, o := range page.Items {
			if o.Size < 0 || o.Size > api.MaxObjectStoragePolicyValue-bytes {
				return objectstorage.ErrInvalid
			}
			bytes += o.Size
			objects++
		}
		if page.NextCursor == "" {
			return st.FinishObjectInventory(ctx, b.ID, token, bytes, objects)
		}
		if seen[page.NextCursor] || len(page.NextCursor) > 8192 {
			return objectstorage.ErrInvalid
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return objectstorage.ErrUnavailable
}

func (s *server) runObjectStorageAccounting(ctx context.Context) {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "faas_object_storage_inventory_scans_total", Help: "Complete or failed object storage inventory scans."}, []string{"outcome"})
	if s.ops != nil {
		s.ops.Registry().MustRegister(counter)
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if s.objectStorage != nil {
			st, ok := s.store.(state.ObjectStorageAccountingStore)
			if ok {
				importCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				err := s.objectStorage.ImportUsageReports(importCtx, func(r api.ObjectStorageUsageReport) error { return st.RecordObjectUsageReport(importCtx, r) })
				cancel()
				if err != nil && ctx.Err() == nil {
					s.log.Warn("object storage provider usage import failed")
				}
			}
		}
		if err := s.reconcileObjectInventories(ctx, func(outcome string) { counter.WithLabelValues(outcome).Inc() }); err != nil && ctx.Err() == nil {
			s.log.Warn("object storage inventory sweep failed")
		}
		ticker.Reset(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
