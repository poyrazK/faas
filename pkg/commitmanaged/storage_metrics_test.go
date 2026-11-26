package commitmanaged

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type receiptFixture struct {
	err   error
	usage state.CommitReceiptUsage
}

func (f *receiptFixture) CommitReceiptStorage(context.Context) (state.CommitReceiptUsage, error) {
	return f.usage, f.err
}
func (f *receiptFixture) ReconcileCommitReceiptStorage(context.Context) (bool, error) {
	return false, f.err
}

type receiptFailureFixture struct{ sampled chan struct{} }

func (f *receiptFailureFixture) CommitReceiptStorage(context.Context) (state.CommitReceiptUsage, error) {
	f.sampled <- struct{}{}
	return state.CommitReceiptUsage{}, nil
}
func (f *receiptFailureFixture) ReconcileCommitReceiptStorage(context.Context) (bool, error) {
	return false, errors.New("batch timed out")
}

func TestReceiptReconciliationFailureIsCounted(t *testing.T) {
	fixture := &receiptFailureFixture{sampled: make(chan struct{}, 1)}
	c := NewReceiptStorageCollector(fixture)
	r := prometheus.NewPedanticRegistry()
	r.MustRegister(c)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	select {
	case <-fixture.sampled:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("collector did not run")
	}
	cancel()
	<-done
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "schedd_commit_receipt_reconciliation_failures_total" {
			if got := family.Metric[0].GetCounter().GetValue(); got != 1 {
				t.Fatalf("reconciliation failures=%v", got)
			}
			return
		}
	}
	t.Fatal("missing reconciliation failure counter")
}

func TestReceiptStorageCollectorFreshFailureAndStale(t *testing.T) {
	now := time.Now()
	fixture := &receiptFixture{usage: state.CommitReceiptUsage{Count: 42, PayloadJSONBytes: 4096, InitializedShards: 256, TotalShards: 256, OldestReconciledAt: &now}}
	c := NewReceiptStorageCollector(fixture)
	r := prometheus.NewPedanticRegistry()
	r.MustRegister(c)
	read := func() map[string]float64 {
		t.Helper()
		families, err := r.Gather()
		if err != nil {
			t.Fatal(err)
		}
		values := make(map[string]float64)
		for _, family := range families {
			if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 {
				t.Fatal("unexpected metric labels")
			}
			if family.Metric[0].Counter != nil {
				values[family.GetName()] = family.Metric[0].GetCounter().GetValue()
			} else {
				values[family.GetName()] = family.Metric[0].GetGauge().GetValue()
			}
		}
		return values
	}
	if got := read(); len(got) != 3 || got["schedd_commit_receipt_storage_sample_success"] != 0 {
		t.Fatalf("initial=%v", got)
	}
	c.sample(t.Context())
	if got := read(); got["schedd_commit_receipt_storage_sample_success"] != 1 || got["schedd_commit_receipt_count"] != 42 || got["schedd_commit_receipt_payload_json_bytes"] != 4096 || got["schedd_commit_receipt_usage_initialized_shards"] != 256 || got["schedd_commit_receipt_usage_reconciliation_current"] != 1 {
		t.Fatalf("fresh=%v", got)
	}
	fixture.err = errors.New("unavailable")
	c.sample(t.Context())
	if got := read(); len(got) != 3 || got["schedd_commit_receipt_storage_sample_success"] != 0 {
		t.Fatalf("failed=%v", got)
	}
	fixture.err = nil
	c.sample(t.Context())
	c.mu.Lock()
	c.checked = time.Now().Add(-31 * time.Minute)
	c.mu.Unlock()
	if got := read(); len(got) != 3 || got["schedd_commit_receipt_storage_sample_success"] != 0 {
		t.Fatalf("stale=%v", got)
	}
	fixture.usage.InitializedShards = 255
	c.sample(t.Context())
	if got := read(); len(got) != 4 || got["schedd_commit_receipt_storage_sample_success"] != 0 || got["schedd_commit_receipt_usage_initialized_shards"] != 255 {
		t.Fatalf("partial backfill=%v", got)
	}
	old := time.Now().Add(-26 * time.Hour)
	fixture.usage.InitializedShards, fixture.usage.OldestReconciledAt = 256, &old
	c.sample(t.Context())
	if got := read(); got["schedd_commit_receipt_storage_sample_success"] != 1 || got["schedd_commit_receipt_usage_reconciliation_current"] != 0 {
		t.Fatalf("reconciliation overdue=%v", got)
	}
}
