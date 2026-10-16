package state

// These tests publish explicit simulated consumer receipts through the real
// store contracts. They exercise adoption authority, never physical KVM effects.
import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardObservationTestStore interface {
	Store
	nativeArtifactTestStore
	ApplicationStandardObservationStore
	ApplicationStandardConsumerRosterStore
	ApplicationStandardLogInventoryStore
	ApplicationStandardEgressStore
	ApplicationStandardSnapshotCaptureStore
	ApplicationStandardOperationControlStore
	ApplicationStandardResourceStore
	ApplicationStandardLogHealthStore
	ApplicationStandardLogDeliveryStore
}

func standardObservationAttempt(t *testing.T, s standardObservationTestStore, app App, reason string) ApplicationStandardOperation {
	t.Helper()
	claim, err := s.ClaimApplicationStandardOperation(t.Context(), "observation-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.ObserveApplicationStandardOperation(t.Context(), claim)
	if err != nil || len(o.Targets) != 1 || o.Targets[0].ErrorCode != reason {
		t.Fatalf("observation: %+v %v; want %s", o, err, reason)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" {
		if o.State != "completed" || o.Targets[0].State != "observed" || e.State != "observed" || e.ObservedRevision != e.DesiredRevision {
			t.Fatalf("complete evidence did not converge: %+v %+v", o, e)
		}
	} else if o.State != "waiting" || o.Targets[0].State != "persisted" || e.State != "persisted" || e.ObservedRevision != 0 {
		t.Fatalf("pending consumers advanced observation: %+v %+v", o, e)
	}
	if _, err := s.ObserveApplicationStandardOperation(t.Context(), claim); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("released generation remained authoritative", err)
	}
	return o
}

func standardObservationLoadLogging(t *testing.T, s standardObservationTestStore, app App) {
	t.Helper()
	roster, err := s.GetApplicationStandardConsumerRoster(t.Context(), app.OrgID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.LoadApplicationStandardLogConsumerSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range roster.Nodes {
		if !node.LoggingRequired {
			continue
		}
		if err := s.HeartbeatComputeNode(t.Context(), node.NodeID); err != nil {
			t.Fatal(err)
		}
		session, err := s.RegisterApplicationStandardLogConsumer(t.Context(), node.NodeID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, inventory := range snapshot.Inventories {
			if sameStandardUUID(inventory.AppID, app.ID) {
				if _, err := s.RecordApplicationStandardLogInventory(t.Context(), session, inventory); err != nil {
					t.Fatal(err)
				}
				found = true
			}
		}
		if !found {
			e, _ := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
			t.Fatalf("fixture omitted current loaded inventory: app=%s state=%s desired=%d persisted=%d inventory_count=%d", app.ID, e.State, e.DesiredRevision, e.PersistedRevision, len(snapshot.Inventories))
		}
	}
}

func standardObservationLifecycle(t *testing.T, s standardObservationTestStore) {
	t.Helper()
	ins, receipt := issueConsumedNativeFixture(t, s)
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	standardObservationAttempt(t, s, app, "logging_consumer_unavailable")
	standardObservationLoadLogging(t, s, app)
	standardObservationAttempt(t, s, app, "egress_observation_pending")
	target := standardEgressPending(t, s, app.ID)
	if _, err := s.RecordApplicationStandardEgress(t.Context(), target, standardEgressReceipt(target)); err != nil {
		t.Fatal(err)
	}
	standardObservationAttempt(t, s, app, "runtime_transition_pending")
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt); err != nil {
		t.Fatal(err)
	}
	standardObservationAttempt(t, s, app, "")
}

func TestMemApplicationStandardObservationLifecycle(t *testing.T) {
	standardObservationLifecycle(t, NewMemStore())
}

func TestApplicationStandardObservationProviderEvidence(t *testing.T) {
	now := time.Now().UTC()
	session := ApplicationStandardLogConsumerSession{NodeID: uuid.NewString(), SessionID: uuid.NewString(), Generation: 1}
	node := ApplicationStandardConsumerNode{LoggingSession: &session}
	binding := ApplicationStandardLogDrainBinding{AppID: uuid.NewString(), DrainID: uuid.NewString(), EffectiveHash: "current"}
	health := ApplicationStandardLogHealthObservation{ApplicationStandardLogDrainBinding: binding, ApplicationStandardLogConsumerSession: session, ApplicationStandardLogHealthEvent: ApplicationStandardLogHealthEvent{Status: "healthy", Reason: "delivered"}, ObservedAt: now, EventAt: now}
	for _, change := range []string{"idle", "degraded", "expired", "old_delivery", "future", "restart", "binding"} {
		t.Run(change, func(t *testing.T) {
			row := health
			expected := "logging_provider_pending"
			switch change {
			case "idle":
				row.Status, row.Reason = "unknown", "idle"
			case "degraded":
				row.Status, row.Reason, expected = "degraded", "retrying", "logging_provider_degraded"
			case "expired":
				row.ObservedAt = now.Add(-api.ApplicationStandardLogHealthFreshness)
			case "old_delivery":
				row.EventAt = now.Add(-api.ApplicationStandardLogHealthFreshness)
			case "future":
				row.ObservedAt = now.Add(time.Second)
			case "restart":
				row.SessionID = uuid.NewString()
			case "binding":
				row.EffectiveHash = "previous"
			}
			if q := standardObservationProvider(node, binding, []ApplicationStandardLogHealthObservation{row}, now); q.reason != expected || !q.until.IsZero() {
				t.Fatalf("stale/unknown provider certified: %+v", q)
			}
		})
	}
	if q := standardObservationProvider(node, binding, []ApplicationStandardLogHealthObservation{health}, now); q.reason != "" || !q.until.After(now) {
		t.Fatal("current delivered provider did not qualify", q)
	}
}

func standardObservationIdleSnapshots(t *testing.T, s standardObservationTestStore) {
	t.Helper()
	ins, req := standardSnapshotFixture(t, s, "warm")
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	standardObservationLoadLogging(t, s, app)
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	ack := standardSnapshotAck(g)
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), ack); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSnapshot(t.Context(), standardSnapshotCacheRow(ins.DeploymentID, g, ack)); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.CreateSnapshot(t.Context(), Snapshot{DeploymentID: ins.DeploymentID, FCVersion: g.FCVersion, StorageKey: "snap/" + ins.DeploymentID + "/unattested.mem", MemBytes: 16384, DiskBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), ins.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	// No live instance exists, yet retained cache remains an obligation.
	standardObservationAttempt(t, s, app, "snapshot_observation_pending")
	if err := s.MarkSnapshotStale(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	standardObservationAttempt(t, s, app, "")
}

func TestMemApplicationStandardObservationIdleSnapshots(t *testing.T) {
	standardObservationIdleSnapshots(t, NewMemStore())
}

func standardObservationPauseFence(t *testing.T, s standardObservationTestStore) {
	t.Helper()
	ins, _ := issueConsumedNativeFixture(t, s)
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimApplicationStandardOperation(t.Context(), "observer-before-pause")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.GetApplicationStandardOperation(t.Context(), app.OrgID, claim.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ControlApplicationStandardOperation(t.Context(), o.OrgID, app.AccountID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ObserveApplicationStandardOperation(t.Context(), claim); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("pause accepted a late observation", err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || e.ObservedRevision != 0 {
		t.Fatal("paused observer changed adoption", err)
	}
}

func TestMemApplicationStandardObservationPauseFence(t *testing.T) {
	standardObservationPauseFence(t, NewMemStore())
}

func standardObservationStoredWaveBit(t *testing.T, s standardMaterializationTestStore, corrupt func(ApplicationStandardOperation)) {
	t.Helper()
	f := newStandardApprovalFixture(t, s)
	o, err := s.ApproveApplicationStandardReview(t.Context(), f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimApplicationStandardOperation(t.Context(), "wave-install")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(t.Context(), claim)
	if err != nil || o.Targets[1].State != "queued" {
		t.Fatal("fixture crossed its first wave", err)
	}
	// Attack fixture: a saved observed bit has no corresponding current evidence.
	// It must not act as permission to install the next target.
	corrupt(o)
	claim, err = s.ClaimApplicationStandardOperation(t.Context(), "wave-restart")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(t.Context(), claim)
	if err != nil || o.State != "waiting" || o.Targets[1].State != "queued" || o.Targets[0].State != "persisted" || o.Targets[0].ErrorCode == "" {
		t.Fatalf("stored bit unlocked a wave: %+v %v", o, err)
	}
}

func TestMemApplicationStandardObservationStoredWaveBit(t *testing.T) {
	m := NewMemStore()
	standardObservationStoredWaveBit(t, m, func(o ApplicationStandardOperation) {
		m.mu.Lock()
		defer m.mu.Unlock()
		stored := m.applicationStandardOperations[o.ID]
		stored.Targets[0].State = "observed"
		m.applicationStandardOperations[o.ID] = stored
	})
}

func standardObservationProviderFixture(t *testing.T, s standardObservationTestStore) (App, Instance) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s)
	destination, err := s.CreateApplicationStandardLogDestination(t.Context(), ApplicationStandardLogDestinationCreate{OrgID: app.OrgID, ActorID: app.AccountID, Name: "Company log provider", Kind: "http_json", TargetURL: "https://logging.example.com/events", AuthHeaderSealed: []byte("sealed-observer-fixture")})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := json.Marshal(map[string]any{"log_destinations": map[string]any{"mode": "mandatory", "value": []string{destination.ID}}, "egress_cidrs": map[string]any{"mode": "mandatory", "value": []string{"8.8.8.0/24"}}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.PublishApplicationStandardVersion(t.Context(), ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: "observed-company-logs", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatal("provider review blocked", err, p.Blockers)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), p.OrgID, app.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimApplicationStandardOperation(t.Context(), "provider-install")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), runtimeArtifactBaseScanInput(in, base)); err != nil {
		t.Fatal(err)
	}
	app, err = s.AppByID(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	publishNativeComposedScan(t, s, app, dep, in.Report)
	ins, binding := nativeArtifactAttempt(t, s, app, dep)
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding.ProtocolVersion, binding.Incarnation = runtimeadmission.ArtifactProtocolVersion, uuid.NewString()
	binding.ArtifactSourcesHash, err = standardCapturedArtifactSourceHash(capture)
	if err != nil {
		t.Fatal(err)
	}
	registerConsumedNativeIdentity(t, s, binding, runtimeadmission.ArtifactProtocolVersion)
	binding, err = s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, binding)
	if err != nil {
		t.Fatal(err)
	}
	ins, err = s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, consumedNativeReceipt(binding, capture))
	if err != nil {
		t.Fatal(err)
	}
	standardObservationLoadLogging(t, s, app)
	target := standardEgressPending(t, s, app.ID)
	if _, err := s.RecordApplicationStandardEgress(t.Context(), target, standardEgressReceipt(target)); err != nil {
		t.Fatal(err)
	}
	return app, ins
}

func standardObservationProviderLifecycle(t *testing.T, s standardObservationTestStore) {
	t.Helper()
	app, ins := standardObservationProviderFixture(t, s)
	standardObservationAttempt(t, s, app, "logging_provider_pending")
	snapshot, err := s.LoadApplicationStandardLogConsumerSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	roster, err := s.GetApplicationStandardConsumerRoster(t.Context(), app.OrgID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var drain AppLogDrain
	for _, d := range snapshot.Drains {
		if sameStandardUUID(d.AppID, app.ID) && d.StandardBinding != nil {
			drain = d
		}
	}
	if drain.ID == "" {
		t.Fatal("managed logging binding missing")
	}
	for i, status := range []string{"unknown", "degraded", "healthy"} {
		for _, node := range roster.Nodes {
			if !node.LoggingRequired || node.LoggingStoppedAt != nil {
				continue
			}
			event := ApplicationStandardLogHealthEvent{EventRevision: int64(i + 1), Status: status, Reason: "idle"}
			if status == "degraded" {
				event.Reason = "delivery_failed"
			}
			if status == "healthy" {
				event.Reason, event.SourceInstanceID, event.Sequence = "delivered", ins.ID, 1
				if _, err := s.RecordApplicationStandardLogDelivery(t.Context(), drain, ins.ID, 1); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.RecordApplicationStandardLogHealth(t.Context(), *node.LoggingSession, drain, event); err != nil {
				t.Fatal(err)
			}
		}
		reason := ""
		if status == "unknown" {
			reason = "logging_provider_pending"
		}
		if status == "degraded" {
			reason = "logging_provider_degraded"
		}
		standardObservationAttempt(t, s, app, reason)
	}
}

func TestMemApplicationStandardObservationProviderLifecycle(t *testing.T) {
	standardObservationProviderLifecycle(t, NewMemStore())
}

func TestMemApplicationStandardObservationLeaseFence(t *testing.T) {
	m := NewMemStore()
	ins, _ := issueConsumedNativeFixture(t, m)
	claim, err := m.ClaimApplicationStandardOperation(t.Context(), "old-observer")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.ObserveApplicationStandardOperation(canceled, claim); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled observer wrote state", err)
	}
	m.mu.Lock()
	held := m.applicationStandardWorkerClaims[claim.OperationID]
	held.Until = time.Now().Add(-time.Second)
	m.applicationStandardWorkerClaims[claim.OperationID] = held
	m.mu.Unlock()
	newClaim, err := m.ClaimApplicationStandardOperation(t.Context(), "new-observer")
	if err != nil || newClaim.Generation <= claim.Generation {
		t.Fatal("expired observer was not fenced", err)
	}
	if _, err := m.ObserveApplicationStandardOperation(t.Context(), claim); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("stolen generation wrote observation", err)
	}
	e := m.applicationStandardEnrollments[ins.AppID]
	if e.ObservedRevision != 0 {
		t.Fatal("fenced observation changed enrollment")
	}
}
