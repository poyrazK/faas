package state

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"
)

func runtimeConfigApplyReceiptMapKey(r RuntimeConfigApplyReceipt) string {
	return strings.Join([]string{r.Key, string(r.Scope), r.ScopeID, r.Daemon, r.Instance}, "\x00")
}

func (m *MemStore) RecordRuntimeConfigApplyReceipt(_ context.Context, receipt RuntimeConfigApplyReceipt) error {
	receipt, err := normalizeRuntimeConfigApplyReceipt(receipt)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := runtimeConfigApplyReceiptMapKey(receipt)
	if existing, ok := m.runtimeConfigApplyReceipts[key]; ok && existing.Version > receipt.Version {
		return nil
	}
	receipt.ReportedAt = time.Now().UTC()
	m.runtimeConfigApplyReceipts[key] = receipt
	return nil
}

func (m *MemStore) ListRuntimeConfigApplyReceipts(_ context.Context, keys []string) ([]RuntimeConfigApplyReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RuntimeConfigApplyReceipt, 0, len(m.runtimeConfigApplyReceipts))
	for _, receipt := range m.runtimeConfigApplyReceipts {
		if len(keys) > 0 && !slices.Contains(keys, receipt.Key) {
			continue
		}
		out = append(out, receipt)
	}
	sort.Slice(out, func(i, j int) bool {
		return runtimeConfigApplyReceiptMapKey(out[i]) < runtimeConfigApplyReceiptMapKey(out[j])
	})
	return out, nil
}
