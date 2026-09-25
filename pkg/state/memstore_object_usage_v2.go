package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectStorageCustomerUsageV2Store = (*MemStore)(nil)

func (m *MemStore) RecordObjectCustomerUsageV2(_ context.Context, report api.ObjectStorageCustomerUsageReportV2) error {
	report = normalizeObjectCustomerUsageV2(report)
	if !validObjectCustomerUsageV2(report, time.Now().UTC()) {
		return ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *api.ObjectStorageCustomerUsageReportV2
	for i := range m.objectCustomerReportsV2 {
		old := &m.objectCustomerReportsV2[i]
		if old.AccountID != report.AccountID || old.BackendID != report.BackendID || !old.PeriodStart.Equal(report.PeriodStart) {
			continue
		}
		if old.ObservedAt.Equal(report.ObservedAt) {
			if sameObjectCustomerUsageV2(*old, report) {
				return nil
			}
			return ErrConflict
		}
		if latest == nil || old.ObservedAt.After(latest.ObservedAt) {
			latest = old
		}
	}
	if !objectCustomerUsageV2Placement(m.objectUsageLocked(report.AccountID, report.PeriodStart), report) {
		return ErrNotFound
	}
	if latest != nil && !objectCustomerUsageV2Advances(*latest, report) {
		return ErrConflict
	}
	m.objectCustomerReportsV2 = append(m.objectCustomerReportsV2, report)
	return nil
}

func (m *MemStore) LatestObjectCustomerUsageV2(_ context.Context, accountID, backendID string, periodStart time.Time) (api.ObjectStorageCustomerUsageReportV2, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *api.ObjectStorageCustomerUsageReportV2
	for i := range m.objectCustomerReportsV2 {
		r := &m.objectCustomerReportsV2[i]
		if r.AccountID == accountID && r.BackendID == backendID && r.PeriodStart.Equal(periodStart) && (latest == nil || r.ObservedAt.After(latest.ObservedAt)) {
			latest = r
		}
	}
	if latest == nil {
		return api.ObjectStorageCustomerUsageReportV2{}, ErrNotFound
	}
	return normalizeObjectCustomerUsageV2(*latest), nil
}
