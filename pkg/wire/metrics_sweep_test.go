package wire

import (
	"net/http/httptest"
	"testing"
)

// This file drives the 0%-coverage OpsMetrics counter/observer
// accessors. Most are simple label-tuple lookups; the test
// constructs a real OpsMetrics and calls each accessor twice
// (with empty + non-empty labels) to flip nil-receiver guards.

func TestSweep_MetricsCounters(t *testing.T) {
	m := NewOpsMetrics("sweep")
	if m == nil {
		t.Fatal("NewOpsMetrics returned nil")
	}
	// Counter accessors — must not panic.
	_ = m.WatchdogKills("Running", "Failed")
	_ = m.WatchdogKills("", "")
	_ = m.LivenessRestarts("app1", "dep1")
	_ = m.LivenessRestarts("", "")
	_ = m.WorkloadOOMKills("app1", "dep1")
	_ = m.WorkloadOOMKills("", "")
	_ = m.WarmSnapshotErrors("missing")
	_ = m.WarmSnapshotErrors("")
	_ = m.EvictedPriority("normal", "scale_down")
	_ = m.EvictedPriority("", "")
	_ = m.EvictionFired("pro", "ram_pressure")
	_ = m.EvictionFired("", "")
	_ = m.RebalanceDecisions("noop")
	_ = m.LiveMigrationDecisions("started")
	_ = m.MigratingReconcileDecisions("drift")
	_ = m.EventsWriteFailures()
	_ = m.AuditWriteFailures("acct")
	_ = m.AccountOrgMismatch("kind")
	_ = m.FailedLoginTotal("1.2.3.4")
	_ = m.FailedLoginDropped()
	_ = m.FailedLoginAuditWriteFailures()
	_ = m.RequestFailure("acct", "/v1/x")
	_ = m.RequestTotal("acct", "/v1/x", "200")
	_ = m.TailCapReached("free")
	_ = m.WakeIDV4Fallback()
	_ = m.SnapshotDiskDrift()
	_ = m.CapacitySignatureRejected()
	_ = m.EgressSourceErrors()
	_ = m.RegistryCredentialMarkUsedFailures()
	_ = m.StorageCacheStaleFallback()
	_ = m.Registry()
}

func TestSweep_MetricsObservers(t *testing.T) {
	m := NewOpsMetrics("sweep")
	// Observer accessors — return prometheus.Observer.
	_ = m.GuestInitDuration("app", "node22")
	_ = m.WakeSnapshotTier("warm")
	_ = m.GuestTailSeconds("free", "node22", "ok")
	_ = m.GuestTailFailedTotal("free", "timeout")
	_ = m.AuditWriteFailureDuration("ok")
	_ = m.CPUStatsCollectDuration()
	_ = m.StripePushDuration("ok")
	_ = m.PaddlePushDuration("ok")
	_ = m.WakePhaseEmitted("admitted", "ok")
}

func TestSweep_MetricsGauges(t *testing.T) {
	m := NewOpsMetrics("sweep")
	_ = m.SSEClients()
}

func TestSweep_MetricsEgress(t *testing.T) {
	m := NewOpsMetrics("sweep")
	_ = m.EgressDeny("10.0.0.0/8", "ipv4")
	_ = m.EgressDenySeries()
	_ = m.OCIEgressDeny("10.0.0.0/8", "ipv4")
	_ = m.OCIEgressDenySeries()
}

func TestSweep_RequestFailureForAndTotalFor(t *testing.T) {
	m := NewOpsMetrics("sweep")
	r := httptest.NewRequest("GET", "/v1/x", nil)
	_ = m.RequestFailureFor(r, "acct")
	_ = m.RequestTotalFor(r, 200, "acct")
}

func TestSweep_NilReceiver(t *testing.T) {
	// nil-receiver guard must not panic on these methods.
	var m *OpsMetrics
	if got := m.LivenessRestarts("", ""); got != nil {
		t.Errorf("nil.LivenessRestarts = %v, want nil", got)
	}
}

func TestSweep_NewOpsMetricsRegistry(t *testing.T) {
	m := NewOpsMetrics("sweep2")
	if m.Registry() == nil {
		t.Error("Registry() returned nil")
	}
}
