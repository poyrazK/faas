package wire_test

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/wire"
)

// productionMetricPrefixes are the prefixes the daemon binaries pass to
// wire.NewOpsMetrics.
var productionMetricPrefixes = []string{
	"apid", "builderd", "gatewayd", "gatewayd_public", "githubd", "imaged",
	"meterd", "outboundd", "profiled", "realtimed", "s3_gateway", "schedd", "vmmd",
}

// singleOwnerMetrics mirrors the owner map in NewOpsMetrics: metric name
// (without the daemon prefix) to the daemons that register it.
var singleOwnerMetrics = map[string][]string{
	"guest_tail_seconds":                                          {"vmmd"},
	"guest_tail_failed_total":                                     {"vmmd"},
	"tail_cap_reached_total":                                      {"vmmd"},
	"guest_init_duration_seconds":                                 {"vmmd"},
	"bridge_framing_total":                                        {"vmmd"},
	"api_hosting_phase_total":                                     {"apid", "builderd", "imaged", "vmmd"},
	"api_hosting_phase_duration_seconds":                          {"apid", "builderd", "imaged", "vmmd"},
	"oci_pull_duration_seconds":                                   {"builderd", "imaged", "vmmd"},
	"deploy_stage_duration_seconds":                               {"builderd", "imaged", "vmmd"},
	"stripe_push_duration_seconds":                                {"apid", "meterd"},
	"paddle_push_duration_seconds":                                {"apid", "meterd"},
	"polar_push_duration_seconds":                                 {"apid", "meterd"},
	"metered_mb_seconds_total":                                    {"apid", "meterd"},
	"build_duration_seconds":                                      {"builderd"},
	"drain_wait_seconds":                                          {"gatewayd", "gatewayd_public"},
	"write_redirect_total":                                        {"gatewayd"},
	"write_redirect_latency_seconds":                              {"gatewayd"},
	"execution_active":                                            {"schedd"},
	"execution_total":                                             {"schedd"},
	"execution_phase_duration_seconds":                            {"schedd"},
	"execution_failures_total":                                    {"schedd"},
	"execution_output_bytes_total":                                {"schedd"},
	"execution_sweeps_total":                                      {"schedd"},
	"execution_queue_depth":                                       {"schedd"},
	"execution_queue_oldest_wait_seconds":                         {"schedd"},
	"execution_workers":                                           {"schedd"},
	"wake_rpc_duration_seconds":                                   {"schedd"},
	"service_rollout_handoff_phase_duration_seconds":              {"schedd"},
	"worker_recovery_apps":                                        {"schedd"},
	"worker_recovery_replicas":                                    {"schedd"},
	"worker_recovery_observed_timestamp_seconds":                  {"schedd"},
	"service_replicas":                                            {"schedd"},
	"esm_polls_total":                                             {"schedd"},
	"esm_records_consumed_total":                                  {"schedd"},
	"esm_lag_seconds":                                             {"schedd"},
	"esm_records_outcome_total":                                   {"schedd"},
	"esm_record_processing_seconds":                               {"schedd"},
	"esm_consumer_lag_messages":                                   {"schedd"},
	"esm_consumer_lag_age_seconds":                                {"schedd"},
	"cron_fire_now_dispatch_duration_seconds":                     {"schedd"},
	"delayed_task_dispatch_total":                                 {"schedd"},
	"delayed_task_schedule_lag_seconds":                           {"schedd"},
	"eviction_fired_total":                                        {"schedd"},
	"scale_up_admit_rps":                                          {"schedd"},
	"events_write_failures_total":                                 {"schedd"},
	"wake_phase_emitted_total":                                    {"apid", "builderd", "gatewayd", "schedd", "vmmd"},
	"wake_identity_invalid_total":                                 {"apid", "builderd", "gatewayd", "schedd", "vmmd"},
	"wake_phase_duration_seconds":                                 {"apid", "builderd", "gatewayd", "schedd"},
	"wake_event_write_duration_seconds":                           {"vmmd"},
	"recovery_event_emitted_total":                                {"apid", "builderd", "gatewayd", "schedd", "vmmd"},
	"audit_write_failures_total":                                  {"apid", "githubd", "meterd", "schedd"},
	"audit_write_failures_duration_seconds":                       {"apid", "githubd", "meterd", "schedd"},
	"audit_log_write_total":                                       {"apid", "githubd", "meterd", "schedd"},
	"audit_log_write_failures_total":                              {"apid", "githubd", "meterd", "schedd"},
	"canary_progression_advanced_total":                           {"meterd"},
	"canary_progression_errors_total":                             {"meterd"},
	"canary_progression_circuit_breaker_total":                    {"meterd"},
	"canary_progression_zero_timestamp_total":                     {"meterd"},
	"canary_progression_health_gate_blocked_total":                {"meterd"},
	"safedeploy_orchestrator_started_total":                       {"meterd"},
	"safedeploy_orchestrator_completed_total":                     {"meterd"},
	"safedeploy_orchestrator_aborted_total":                       {"meterd"},
	"safedeploy_orchestrator_stuck_detected_total":                {"meterd"},
	"safedeploy_orchestrator_audit_emit_failed_total":             {"meterd"},
	"safedeploy_orchestrator_stuck_check_missing_timestamp_total": {"meterd"},
	"safedeploy_orchestrator_auto_aborted_total":                  {"meterd"},
	"safedeploy_orchestrator_auto_abort_failed_total":             {"meterd"},
	"safedeploy_in_flight_rollouts":                               {"meterd"},
	"deployment_audit_emitted_total":                              {"meterd"},
	"deployment_audit_gc_failed_total":                            {"meterd"},
	"deployment_audit_gc_rows_deleted_total":                      {"meterd"},
	"canary_stuck_step_alert_fired_total":                         {"meterd"},
	"safedeploy_audit_emit_failing_alert_fired_total":             {"meterd"},
	"deployment_audit_gc_failing_alert_fired_total":               {"meterd"},
	"canary_fleet_in_flight_high_alert_fired_total":               {"meterd"},
}

// singleOwnerAccessorCallers lists, for each accessor of a single-owner
// collector, the packages that call it outside pkg/wire. The owners above
// are the daemons whose binaries run these packages.
var singleOwnerAccessorCallers = map[string][]string{
	"GuestTailSeconds":                                      nil,
	"GuestTailFailedTotal":                                  nil,
	"TailCapReached":                                        nil,
	"GuestInitDuration":                                     {"pkg/vmmdgrpc"},
	"BridgeFramingTotal":                                    {"pkg/vmmdgrpc"},
	"ObserveAPIHostingPhase":                                {"pkg/apid/apidserver", "pkg/apid/apidsource", "pkg/imaged"},
	"ObserveImagedOCIPull":                                  {"pkg/imaged"},
	"ObserveDeployStageDuration":                            {"pkg/imaged"},
	"StripePushDuration":                                    {"pkg/meter"},
	"PaddlePushDuration":                                    {"pkg/meter"},
	"PolarPushDuration":                                     {"pkg/meter"},
	"MeteredMBSecondsTotal":                                 {"pkg/meter"},
	"DeploymentAuditEmittedTotal":                           {"pkg/safedeploy"},
	"ObserveBuildDuration":                                  {"pkg/builderd"},
	"ObserveDrainWait":                                      {"cmd/gatewayd-internal", "cmd/gatewayd-public"},
	"WriteRedirectTotal":                                    {"cmd/gatewayd-internal"},
	"WriteRedirectLatency":                                  {"cmd/gatewayd-internal"},
	"RecordExecutionStarted":                                {"pkg/sched"},
	"RecordExecutionFinished":                               {"pkg/sched"},
	"RecordExecutionTerminal":                               {"pkg/sched"},
	"ObserveExecutionPhase":                                 {"pkg/sched"},
	"RecordExecutionFailure":                                {"pkg/sched"},
	"ObserveExecutionOutput":                                {"pkg/sched"},
	"RecordExecutionSweep":                                  {"pkg/sched"},
	"SetExecutionQueue":                                     {"pkg/sched"},
	"SetExecutionWorkers":                                   {"pkg/sched"},
	"WakeRPCDuration":                                       {"pkg/sched"},
	"ObserveServiceRolloutHandoffPhase":                     {"pkg/sched"},
	"SetWorkerRecoverySummary":                              {"pkg/sched"},
	"SetServiceReplicaStatus":                               {"pkg/sched"},
	"ObserveESMPoll":                                        {"pkg/sched"},
	"ObserveESMRecords":                                     {"pkg/sched"},
	"ObserveESMLag":                                         {"pkg/sched"},
	"ObserveESMRecordOutcome":                               {"pkg/sched"},
	"ObserveESMRecordProcessing":                            {"pkg/sched"},
	"ObserveESMConsumerLag":                                 {"pkg/sched"},
	"CronFireNowDispatchDuration":                           {"pkg/sched"},
	"ObserveDelayedTaskDispatch":                            {"pkg/sched"},
	"ObserveDelayedTaskScheduleLag":                         {"pkg/sched"},
	"EvictionFired":                                         {"pkg/sched"},
	"ObserveScaleUpAdmitRPS":                                {"pkg/sched/scaleup"},
	"EventsWriteFailures":                                   {"pkg/sched"},
	"WakePhaseEmitted":                                      {"pkg/events"},
	"WakeIdentityInvalid":                                   {"pkg/events"},
	"WakePhaseDuration":                                     {"pkg/events"},
	"RecoveryEventEmitted":                                  {"pkg/events"},
	"AuditWriteFailures":                                    {"pkg/audit"},
	"AuditLogWriteTotal":                                    {"pkg/audit"},
	"AuditLogWriteFailuresTotal":                            {"pkg/audit"},
	"AuditWriteFailureDuration":                             {"pkg/apid/apidserver", "pkg/audit"},
	"CanaryProgressionAdvancedTotal":                        {"pkg/canary"},
	"CanaryProgressionErrorsTotal":                          {"pkg/canary"},
	"CanaryProgressionCircuitBreakerTotal":                  {"pkg/canary"},
	"CanaryProgressionZeroTimestampTotal":                   {"pkg/canary"},
	"CanaryProgressionHealthGateBlockedTotal":               {"pkg/canary"},
	"SafedeployOrchestratorStartedTotal":                    {"pkg/safedeploy"},
	"SafedeployOrchestratorCompletedTotal":                  {"pkg/safedeploy"},
	"SafedeployOrchestratorAbortedTotal":                    {"pkg/safedeploy"},
	"SafedeployOrchestratorStuckDetectedTotal":              {"pkg/safedeploy"},
	"SafedeployOrchestratorAuditEmitFailedTotal":            {"pkg/safedeploy"},
	"SafedeployOrchestratorStuckCheckMissingTimestampTotal": {"pkg/safedeploy"},
	"SafedeployOrchestratorAutoAbortedTotal":                {"pkg/safedeploy"},
	"SafedeployOrchestratorAutoAbortFailedTotal":            {"pkg/safedeploy"},
	"SafedeployInFlightRollouts":                            {"pkg/safedeploy"},
	"DeploymentAuditGCFailedTotal":                          {"cmd/meterd"},
	"DeploymentAuditGCRowsDeleted":                          {"cmd/meterd"},
	"CanaryStuckStepAlertFiredOp":                           nil,
	"CanaryStuckStepAlertFiredTotal":                        nil,
	"SafedeployAuditEmitFailingAlertFiredOp":                nil,
	"SafedeployAuditEmitFailingAlertFiredTotal":             nil,
	"DeploymentAuditGCFailingAlertFiredOp":                  nil,
	"DeploymentAuditGCFailingAlertFiredTotal":               nil,
	"CanaryFleetInFlightHighAlertFiredOp":                   nil,
	"CanaryFleetInFlightHighAlertFiredTotal":                nil,
}

// TestSingleOwnerCollectorsRegisterOnOwners pins the owner map in
// NewOpsMetrics: each single-owner metric is registered on exactly its
// owners, and a non-daemon prefix (tests, tools) still registers all.
// spec: §12
func TestSingleOwnerCollectorsRegisterOnOwners(t *testing.T) {
	for _, prefix := range productionMetricPrefixes {
		m := wire.NewOpsMetrics(prefix)
		for metric, owners := range singleOwnerMetrics {
			want := containsName(owners, prefix)
			if got := isRegistered(m.Registry(), prefix+"_"+metric); got != want {
				t.Errorf("%s_%s registered=%v, want %v (owners %v)", prefix, metric, got, want, owners)
			}
		}
	}
	m := wire.NewOpsMetrics("owner_test")
	for metric := range singleOwnerMetrics {
		if metric == "wake_event_write_duration_seconds" {
			continue // vmmd's name for the events histogram; every other prefix uses wake_phase_duration_seconds
		}
		if !isRegistered(m.Registry(), "owner_test_"+metric) {
			t.Errorf("owner_test_%s is not registered; non-daemon prefixes must keep every collector", metric)
		}
	}
}

// TestSingleOwnerAccessorsHaveKnownCallers fails when an accessor of a
// single-owner collector gains a caller package. A new caller may run in a
// daemon that does not register the collector, where its writes would never
// be exported: add that daemon to the owner map in NewOpsMetrics and to
// singleOwnerMetrics, then list the package here.
func TestSingleOwnerAccessorsHaveKnownCallers(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string]map[string]bool{}
	for _, dir := range []string{"cmd", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || filepath.ToSlash(path) == filepath.ToSlash(filepath.Join(root, "pkg", "wire")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			for _, accessor := range selectorCalls(t, path) {
				if _, tracked := singleOwnerAccessorCallers[accessor]; tracked {
					if found[accessor] == nil {
						found[accessor] = map[string]bool{}
					}
					found[accessor][filepath.ToSlash(rel)] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	for accessor, allowed := range singleOwnerAccessorCallers {
		var unexpected []string
		for pkg := range found[accessor] {
			if !containsName(allowed, pkg) {
				unexpected = append(unexpected, pkg)
			}
		}
		sort.Strings(unexpected)
		for _, pkg := range unexpected {
			t.Errorf("%s now calls %s: register its collector on the daemons that run %s (owner map in NewOpsMetrics) and list the package in singleOwnerAccessorCallers", pkg, accessor, pkg)
		}
		for _, pkg := range allowed {
			if !found[accessor][pkg] {
				t.Errorf("singleOwnerAccessorCallers[%q] lists %s, which no longer calls it; drop it and recheck the collector's owners", accessor, pkg)
			}
		}
	}
}

// selectorCalls returns every identifier that follows a '.' in the file's
// code, ignoring comments and string literals.
func selectorCalls(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fset := token.NewFileSet()
	var sc scanner.Scanner
	sc.Init(fset.AddFile(path, fset.Base(), len(raw)), raw, nil, 0)
	var names []string
	afterPeriod := false
	for {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			return names
		}
		if afterPeriod && tok == token.IDENT {
			names = append(names, lit)
		}
		afterPeriod = tok == token.PERIOD
	}
}

// isRegistered reports whether reg already holds a collector named name:
// registering a probe with that name and a different help text fails only
// when the name is taken.
func isRegistered(reg *prometheus.Registry, name string) bool {
	probe := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: "registration probe"})
	if err := reg.Register(probe); err != nil {
		return true
	}
	reg.Unregister(probe)
	return false
}

func containsName(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// TestIdentityRowsCoverOnlyTheOwnDaemon pins that a production daemon
// pre-creates build-info, uptime and readiness rows for itself only. Rows for
// the other twelve names rendered on the fleet dashboard as daemons that were
// never ready, had zero uptime and ran this binary's version.
// spec: §12
func TestIdentityRowsCoverOnlyTheOwnDaemon(t *testing.T) {
	own := map[string]string{
		"apid": "apid", "builderd": "builderd", "gatewayd": "gatewayd-internal",
		"gatewayd_public": "gatewayd-public", "githubd": "githubd", "imaged": "imaged",
		"meterd": "meterd", "outboundd": "outboundd", "profiled": "profiled",
		"realtimed": "realtimed", "s3_gateway": "s3-gatewayd", "schedd": "schedd", "vmmd": "vmmd",
	}
	for _, prefix := range productionMetricPrefixes {
		daemons := daemonLabelValues(t, wire.NewOpsMetrics(prefix).Registry(), prefix+"_daemon_ready")
		if len(daemons) != 1 || daemons[0] != own[prefix] {
			t.Errorf("%s_daemon_ready daemons = %v, want [%s]", prefix, daemons, own[prefix])
		}
	}
	if daemons := daemonLabelValues(t, wire.NewOpsMetrics("owner_test").Registry(), "owner_test_daemon_ready"); len(daemons) < 13 {
		t.Errorf("owner_test_daemon_ready daemons = %v; non-daemon prefixes keep every row", daemons)
	}
}

// TestMarkReadyReportsRealtimedAndS3Gateway pins the two daemons the closed
// label set used to miss: both reported as daemon="other", so the
// FaasDaemonNotReady branches for them could never match.
// spec: §12
func TestMarkReadyReportsRealtimedAndS3Gateway(t *testing.T) {
	for prefix, daemon := range map[string]string{"realtimed": "realtimed", "s3_gateway": "s3-gatewayd"} {
		m := wire.NewOpsMetrics(prefix)
		m.MarkReady(daemon, true, "")
		got := daemonLabelValues(t, m.Registry(), prefix+"_daemon_ready")
		if len(got) != 1 || got[0] != daemon {
			t.Errorf("%s_daemon_ready daemons after MarkReady(%q) = %v, want [%s]", prefix, daemon, got, daemon)
		}
	}
}

func daemonLabelValues(t *testing.T, reg *prometheus.Registry, name string) []string {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var daemons []string
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, metric := range f.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "daemon" {
					daemons = append(daemons, label.GetValue())
				}
			}
		}
	}
	sort.Strings(daemons)
	return daemons
}
