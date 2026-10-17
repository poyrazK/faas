package functionmigration

// adr: 1352
// These synthetic documents test rejection rules only; they supply no native
// execution evidence and are never written as qualification artifacts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func witnessConfig() Config {
	c := Config{Version: 1, FixtureID: "11111111-1111-4111-8111-111111111111", Revision: strings.Repeat("a", 40),
		Catalog: "faas_function_migration_native_11111111111141118111111111111111", APIURL: "http://127.0.0.1:18080", GatewayURL: "http://127.0.0.1:18081",
		AppDomain: "fixture.example", SQLIP: "1.1.1.2", SQLCAPath: "/fixture/sql-ca.pem", FCVersion: "test-only", TestBinarySHA: strings.Repeat("b", 64), Builds: map[string]string{}}
	for _, name := range AssetNames() {
		c.Builds[name] = strings.Repeat("c", 64)
	}
	for i, id := range []string{"22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"} {
		h := Host{NodeID: id, SSH: "root@fixture-" + id, RemoteSocket: "/run/faas/vmmd.sock", LocalSocket: "/fixture/" + id + ".sock", Assets: map[string]string{}}
		h.Proof = HostProof{Version: 1, FixtureID: c.FixtureID, Revision: c.Revision, Catalog: c.Catalog, NodeID: id, Root: true, NativeX86: true, KVM: true,
			MachineSHA: strings.Repeat(string(rune('d'+i)), 64), BootSHA: strings.Repeat(string(rune('f'-i)), 64), VMMDPID: 10, ScheddPID: 11,
			VMMDStart: "12", ScheddStart: "13", RunningVMMD: c.Builds["vmmd"], RunningSchedd: c.Builds["schedd"], Assets: map[string]string{}}
		for _, name := range AssetNames() {
			h.Assets[name], h.Proof.Assets[name] = "/fixture/"+name, c.Builds[name]
		}
		c.Hosts = append(c.Hosts, h)
	}
	return c
}

func witnessReport() Report {
	c := witnessConfig()
	r := Report{Version: ReportVersion, Scope: ReportScope, Config: c, BothHostsLeakChecked: true}
	for _, runtime := range []string{"node22", "node24"} {
		for _, scenario := range Scenarios() {
			run := Run{Runtime: runtime, Scenario: scenario, InstanceID: uuid.NewSHA1(uuid.Nil, []byte(runtime+scenario)).String(), SourceNode: c.Hosts[0].NodeID, DestinationNode: c.Hosts[1].NodeID,
				SourceWake: "55555555-5555-4555-8555-555555555555", DestinationWake: "66666666-6666-4666-8666-666666666666",
				RootfsInputSHA: strings.Repeat("a", 64), BaseInputSHA: strings.Repeat("b", 64),
				Source:      Owner{PID: 123, Start: "456", UID: 20001, Netns: "faas-test", ReceiptSHA: strings.Repeat("a", 64), Verified: true},
				Destination: Owner{PID: 123, Start: "456", UID: 20001, Netns: "faas-test", ReceiptSHA: strings.Repeat("b", 64), Verified: true},
				RowsBefore:  map[string]int{"one": 100, "two": 101, "three": 102}, RowsAfter: map[string]int{"one": 100, "two": 101, "three": 102},
				CaptureHeld: true, DrainedBeforeCapture: true, RecoveryHeld: true, ReadyKeptSource: true, ExecutionRefused: true, CommitVerified: true,
				SourceRetired: true, SourceLeaseRemoved: true, FinalAppRetired: true}
			run.DestinationWake = uuid.NewSHA1(uuid.Nil, []byte(runtime+scenario+"wake")).String()
			for i := range 4 {
				run.CallsAtReady = append(run.CallsAtReady, Calls{PID: 100 + i, Checkpoints: 1, Recoveries: 1})
				run.CallsAfter = append(run.CallsAfter, Calls{PID: 100 + i, Checkpoints: 1, Recoveries: 1})
				run.CheckpointWorkers = append(run.CheckpointWorkers, Worker{PID: 100 + i, Generations: []int{0, 1}})
				run.RecoveryWorkers = append(run.RecoveryWorkers, Worker{PID: 100 + i, Generations: []int{1, 2}})
				for j := range 2 {
					run.SourceSQL = append(run.SourceSQL, Session{PID: 200 + i*2 + j, WorkerPID: 100 + i, StartedAt: "2026-10-17T10:00:00Z", TLS: true})
					run.DestinationSQL = append(run.DestinationSQL, Session{PID: 300 + i*2 + j, WorkerPID: 100 + i, StartedAt: "2026-10-17T10:01:00Z", TLS: true})
				}
			}
			if scenario == LostCommit {
				run.Fault = &ReplyFault{Mode: "post-commit-error", CommitDiscards: 1, RecoveredDecisions: 1}
			}
			if RestartScenario(scenario) {
				phase := "ready"
				if scenario == RestartCommitted {
					phase = "committed"
				}
				run.Restart = &RestartEvidence{Before: CoordinatorIdentity{PID: 20, Start: "30", BinarySHA: c.TestBinarySHA}, After: CoordinatorIdentity{PID: 21, Start: "31", BinarySHA: c.TestBinarySHA},
					SIGKILLVerified: true, LeaseRetained: true, PendingRetained: scenario == RestartReady, CommittedAtCrash: scenario == RestartCommitted, ExecutionClosed: true,
					SourceAtCrash: run.Source, DestinationAfter: run.Destination, SQLAfter: append([]Session{}, run.DestinationSQL...), Startup: CoordinatorEvent{Phase: "recovered", Completed: 1, Seeded: true, DiscoveredPhase: phase}}
			}
			if scenario == LostReadiness || scenario == Duplicate {
				copies := 3
				fault := &ReplyFault{Mode: "explicit-concurrent-retry", OwnerAfterReplays: run.Destination, SQLAfterReplays: append([]Session{}, run.DestinationSQL...),
					SourceKeptAfterReplay: true, ClosedAfterReplay: true, Replays: &Replays{RequestSHA: strings.Repeat("c", 64), ReplySHA: strings.Repeat("d", 64)}}
				if scenario == LostReadiness {
					copies, fault.Mode, fault.ReadinessDiscards = 1, "client-reply-discard", 1
				}
				for range copies {
					fault.Replays.Requests = append(fault.Replays.Requests, fault.Replays.RequestSHA)
					fault.Replays.Replies = append(fault.Replays.Replies, fault.Replays.ReplySHA)
				}
				run.Fault = fault
			}
			r.Runs = append(r.Runs, run)
		}
	}
	return r
}

func TestReportRejectsIncompleteOrReplayedEvidence(t *testing.T) {
	if err := witnessReport().Check(); err != nil {
		t.Fatal("valid witness document rejected", err)
	}
	for _, tc := range []struct {
		name string
		bad  func(*Report)
	}{
		{"empty", func(r *Report) { *r = Report{} }},
		{"runtime skipped", func(r *Report) { r.Runs = r.Runs[:1] }},
		{"duplicate runtime", func(r *Report) { r.Runs[len(Scenarios())].Runtime = "node22" }},
		{"legacy two-case scope", func(r *Report) { r.Version, r.Scope = 1, "native-successful-migration-v1" }},
		{"relabelled duplicate instance", func(r *Report) { r.Runs[1].InstanceID = r.Runs[0].InstanceID }},
		{"baseline claims a fault", func(r *Report) { r.Runs[0].Fault = r.Runs[1].Fault }},
		{"missing readiness discard", func(r *Report) { r.Runs[1].Fault.ReadinessDiscards = 0 }},
		{"missing native retry", func(r *Report) { r.Runs[1].Fault.Replays = nil }},
		{"retry changes original request", func(r *Report) { r.Runs[1].Fault.Replays.Requests[0] = strings.Repeat("f", 64) }},
		{"retry changes physical reply", func(r *Report) { r.Runs[1].Fault.Replays.Replies[0] = strings.Repeat("f", 64) }},
		{"retry replaces native process", func(r *Report) { r.Runs[1].Fault.OwnerAfterReplays.PID++ }},
		{"retry reconnects SQL", func(r *Report) { r.Runs[1].Fault.SQLAfterReplays[0].PID++ }},
		{"retry publishes ownership", func(r *Report) { r.Runs[1].Fault.SourceKeptAfterReplay = false }},
		{"retry opens execution", func(r *Report) { r.Runs[1].Fault.ClosedAfterReplay = false }},
		{"commit not discarded", func(r *Report) { r.Runs[2].Fault.CommitDiscards = 0 }},
		{"commit decision not read", func(r *Report) { r.Runs[2].Fault.RecoveredDecisions = 0 }},
		{"duplicate adoption omitted", func(r *Report) { r.Runs[3].Fault.Replays.Requests = r.Runs[3].Fault.Replays.Requests[:1] }},
		{"previous reply-only scope", func(r *Report) { r.Version, r.Scope = 2, "native-migration-reply-boundaries-v1" }},
		{"restart omitted", func(r *Report) { r.Runs[4].Restart = nil }},
		{"same coordinator process", func(r *Report) { r.Runs[4].Restart.After = r.Runs[4].Restart.Before }},
		{"different coordinator binary", func(r *Report) { r.Runs[4].Restart.After.BinarySHA = strings.Repeat("f", 64) }},
		{"unconfirmed crash", func(r *Report) { r.Runs[4].Restart.SIGKILLVerified = false }},
		{"lost pending destination", func(r *Report) { r.Runs[4].Restart.PendingRetained = false }},
		{"wrong crash boundary", func(r *Report) { r.Runs[5].Restart.CommittedAtCrash = false }},
		{"crash opens execution", func(r *Report) { r.Runs[5].Restart.ExecutionClosed = false }},
		{"startup not discovered", func(r *Report) { r.Runs[4].Restart.Startup.Completed = 0 }},
		{"startup skipped ledger", func(r *Report) { r.Runs[4].Restart.Startup.Seeded = false }},
		{"startup changes physical owner", func(r *Report) { r.Runs[4].Restart.DestinationAfter.PID++ }},
		{"startup replaces pools", func(r *Report) { r.Runs[5].Restart.SQLAfter[0].PID++ }},
		{"callback replay after ACK", func(r *Report) { r.Runs[3].CallsAfter[0].Recoveries++ }},
		{"callback replay before ready", func(r *Report) { r.Runs[1].CallsAtReady[0].Recoveries++ }},
		{"unsupported production claim", func(r *Report) { r.ProductionReady = true }},
		{"unsupported failure matrix claim", func(r *Report) { r.FailureMatrixQualified = true }},
		{"missing host leak check", func(r *Report) { r.BothHostsLeakChecked = false }},
		{"SSH alias", func(r *Report) { r.Config.Hosts[1].Proof.MachineSHA = r.Config.Hosts[0].Proof.MachineSHA }},
		{"nested host", func(r *Report) { r.Config.Hosts[0].Proof.NativeX86 = false }},
		{"stale daemon", func(r *Report) { r.Config.Hosts[0].Proof.RunningVMMD = strings.Repeat("f", 64) }},
		{"stale runner", func(r *Report) { r.Config.Hosts[0].Proof.Assets["node24"] = strings.Repeat("f", 64) }},
		{"unmeasured producer", func(r *Report) { r.Runs[0].RootfsInputSHA = "" }},
		{"unscoped catalog", func(r *Report) { r.Config.Catalog = "faas" }},
		{"unverified process", func(r *Report) { r.Runs[0].Source.Verified = false }},
		{"same receipt", func(r *Report) { r.Runs[0].Destination.ReceiptSHA = r.Runs[0].Source.ReceiptSHA }},
		{"same node", func(r *Report) { r.Runs[0].DestinationNode = r.Runs[0].SourceNode }},
		{"capture raced witness", func(r *Report) { r.Runs[0].CaptureHeld = false }},
		{"undrained pools", func(r *Report) { r.Runs[0].DrainedBeforeCapture = false }},
		{"missing recovery barrier", func(r *Report) { r.Runs[0].RecoveryHeld = false }},
		{"early ownership", func(r *Report) { r.Runs[0].ReadyKeptSource = false }},
		{"early execution", func(r *Report) { r.Runs[0].ExecutionRefused = false }},
		{"missing commit", func(r *Report) { r.Runs[0].CommitVerified = false }},
		{"unretired source", func(r *Report) { r.Runs[0].SourceRetired = false }},
		{"unremoved lease", func(r *Report) { r.Runs[0].SourceLeaseRemoved = false }},
		{"failed cleanup", func(r *Report) { r.Runs[0].FinalAppRetired = false }},
		{"row replay", func(r *Report) { r.Runs[0].RowsAfter["before-commit"] = 100 }},
		{"unknown writer", func(r *Report) { r.Runs[0].RowsBefore["one"], r.Runs[0].RowsAfter["one"] = 999, 999 }},
		{"missing pool", func(r *Report) { r.Runs[0].DestinationSQL = r.Runs[0].DestinationSQL[:7] }},
		{"plain SQL", func(r *Report) { r.Runs[0].SourceSQL[0].TLS = false }},
		{"reused session", func(r *Report) { r.Runs[0].DestinationSQL[0] = r.Runs[0].SourceSQL[0] }},
		{"invalid backend generation", func(r *Report) { r.Runs[0].SourceSQL[0].StartedAt = "not-a-time" }},
		{"duplicate backend", func(r *Report) { r.Runs[0].SourceSQL[1] = r.Runs[0].SourceSQL[0] }},
		{"missed interpreter", func(r *Report) { r.Runs[0].RecoveryWorkers[0].PID = 999 }},
		{"replayed callback", func(r *Report) { r.Runs[0].RecoveryWorkers[0].Generations[0]++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := witnessReport()
			tc.bad(&r)
			if r.Check() == nil {
				t.Fatal("incomplete evidence accepted")
			}
		})
	}
}

func TestLoadRejectsUnknownOrTrailingConfiguration(t *testing.T) {
	data, err := json.Marshal(witnessConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"valid", data},
		{"unknown", append([]byte(`{"unexpected":true,`), data[1:]...)},
		{"trailing", append(append([]byte{}, data...), []byte(` {}`)...)},
		{"empty", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if (err == nil) != (tc.name == "valid") {
				t.Fatalf("unexpected configuration result: %v", err)
			}
		})
	}
}
