package state

// adr: 581. Native and provider facts here are explicit simulated store evidence.

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

type standardAutomaticObservationTestStore interface {
	standardObservationTestStore
	ApplicationStandardAutomaticMaterializationStore
	ApplicationStandardAutomaticObservationStore
	ApplicationStandardLocalIntentStore
}

func automaticObservationAdmission(t *testing.T, s standardAutomaticObservationTestStore, owner CreateAccountWithPersonalOrgResult) {
	t.Helper()
	v, err := s.PublishApplicationStandardVersion(t.Context(), ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "automatic-observer", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]},"security_policy":{"mode":"mandatory","value":"warn"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), owner.PersonalOrg.ID, owner.Account.ID, ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 || len(p.Applications) != 0 {
		t.Fatal("empty organization admission failed", err)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), p.OrgID, owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "automatic-admission")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c)
	if err != nil || o.State != "completed" || len(o.Targets) != 0 {
		t.Fatal("admission did not complete before creation", err)
	}
}

func automaticObservationFixture(t *testing.T, s standardAutomaticObservationTestStore) (App, Instance) {
	t.Helper()
	in, base, app, dep := artifactScanBaseFixture(t, s, registryFixtureCreateHooks{
		before: func(owner CreateAccountWithPersonalOrgResult) { automaticObservationAdmission(t, s, owner) },
		after: func(App) {
			if e := automaticRepair(t, s); e.State != "persisted" || e.ObservedRevision != 0 {
				t.Fatal("automatic installation fabricated observation")
			}
		},
	})
	app, err := s.AppByID(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageScan(t.Context(), runtimeArtifactBaseScanInput(in, base)); err != nil {
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
	return app, ins
}

func automaticObservationAttempt(t *testing.T, s standardAutomaticObservationTestStore, app App, reason string) ApplicationStandardEnrollment {
	t.Helper()
	c, err := s.ClaimApplicationStandardObservation(t.Context(), "automatic-observer-"+uuid.NewString())
	if err != nil || !sameStandardUUID(c.AppID, app.ID) {
		t.Fatal("automatic observation claim", err)
	}
	e, err := s.ObserveApplicationStandardEnrollment(t.Context(), c)
	if err != nil || e.ErrorCode != reason || e.PersistedRevision != e.DesiredRevision {
		t.Fatalf("automatic observation: %+v %v; want %s", e, err, reason)
	}
	if reason == "" && (e.State != "observed" || e.ObservedRevision != e.DesiredRevision) || reason != "" && (e.State != "persisted" || e.ObservedRevision != 0) {
		t.Fatal("automatic observation fabricated or lost evidence", e)
	}
	if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), c); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("released observation claim remained valid", err)
	}
	return e
}

func automaticObservationLifecycle(t *testing.T, s standardAutomaticObservationTestStore, due func(string)) {
	t.Helper()
	app, ins := automaticObservationFixture(t, s)
	automaticObservationAttempt(t, s, app, "logging_consumer_unavailable")
	if _, err := s.ClaimApplicationStandardObservation(t.Context(), "same-pass"); !errors.Is(err, ErrNotFound) {
		t.Fatal("same application consumed the bounded pass", err)
	}
	standardObservationLoadLogging(t, s, app)
	due(app.ID)
	automaticObservationAttempt(t, s, app, "egress_observation_pending")
	target := standardEgressPending(t, s, app.ID)
	if _, err := s.RecordApplicationStandardEgress(t.Context(), target, standardEgressReceipt(target)); err != nil {
		t.Fatal(err)
	}
	due(app.ID)
	e := automaticObservationAttempt(t, s, app, "")
	due(app.ID)
	refreshed := automaticObservationAttempt(t, s, app, "")
	if !refreshed.UpdatedAt.Equal(e.UpdatedAt) || refreshed.DesiredRevision != e.DesiredRevision || refreshed.EffectiveHash != e.EffectiveHash {
		t.Fatal("healthy recheck changed intent or its fingerprint")
	}
	// A restarted gateway's old inventory cannot keep the app observed.
	if _, err := s.RegisterApplicationStandardLogConsumer(t.Context(), ins.NodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	due(app.ID)
	automaticObservationAttempt(t, s, app, "logging_inventory_pending")
	standardObservationLoadLogging(t, s, app)
	due(app.ID)
	automaticObservationAttempt(t, s, app, "")
	// Membership additions are discovered automatically, without manual enrollment.
	if _, err := s.CreateComputeNode(t.Context(), ComputeNode{Name: "automatic-new-node-" + uuid.NewString(), TargetURL: "unix:///tmp/automatic-observer", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true}); err != nil {
		t.Fatal(err)
	}
	due(app.ID)
	automaticObservationAttempt(t, s, app, "logging_consumer_unavailable")
	standardObservationLoadLogging(t, s, app)
	due(app.ID)
	automaticObservationAttempt(t, s, app, "")
}

func memAutomaticObservationDue(m *MemStore, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	check := m.standardObservationChecks[canonicalStandardUUID(id)]
	check.at = time.Now().Add(-api.ApplicationStandardObservationInterval)
	m.standardObservationChecks[canonicalStandardUUID(id)] = check
}

func TestMemApplicationStandardAutomaticObservationLifecycle(t *testing.T) {
	m := NewMemStore()
	automaticObservationLifecycle(t, m, func(id string) { memAutomaticObservationDue(m, id) })
}

func automaticObservationReviewPrecedence(t *testing.T, s standardAutomaticObservationTestStore) {
	t.Helper()
	ins, _ := issueConsumedNativeFixture(t, s)
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	// The reviewed operation owns persisted as well as queued targets.
	if _, err := s.ClaimApplicationStandardObservation(t.Context(), "automatic-before-pause"); !errors.Is(err, ErrNotFound) {
		t.Fatal("automatic observer stole a reviewed target", err)
	}
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "pause-reviewed-observer")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.GetApplicationStandardOperation(t.Context(), app.OrgID, c.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ControlApplicationStandardOperation(t.Context(), app.OrgID, app.AccountID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimApplicationStandardObservation(t.Context(), "automatic-after-pause"); !errors.Is(err, ErrNotFound) {
		t.Fatal("automatic observer bypassed pause", err)
	}
}

func TestMemApplicationStandardAutomaticObservationReviewPrecedence(t *testing.T) {
	automaticObservationReviewPrecedence(t, NewMemStore())
}

func automaticObservationLeaseFence(t *testing.T, s standardAutomaticObservationTestStore, expire func(ApplicationStandardEnrollmentClaim), due func(string)) {
	t.Helper()
	app, _ := automaticObservationFixture(t, s)
	c, err := s.ClaimApplicationStandardObservation(t.Context(), "old-automatic-observer")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ObserveApplicationStandardEnrollment(canceled, c); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled observer wrote enrollment", err)
	}
	expire(c)
	c.Until = time.Now().Add(time.Hour)
	if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), c); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("caller renewed an expired observation lease", err)
	}
	due(app.ID)
	next, err := s.ClaimApplicationStandardObservation(t.Context(), "new-automatic-observer")
	if err != nil || next.Generation <= c.Generation {
		t.Fatal("observation generation did not advance", err)
	}
	if err := s.ReleaseApplicationStandardEnrollmentWorker(t.Context(), c); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("old observer released another owner's lease", err)
	}
	if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), c); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("old observer wrote another owner's enrollment", err)
	}
	if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), next); err != nil {
		t.Fatal("replacement observer could not make progress", err)
	}
}

func TestMemApplicationStandardAutomaticObservationLeaseFence(t *testing.T) {
	m := NewMemStore()
	automaticObservationLeaseFence(t, m, func(c ApplicationStandardEnrollmentClaim) {
		m.mu.Lock()
		held := m.applicationStandardEnrollmentClaims[c.AppID]
		held.Until = time.Now().Add(-time.Second)
		m.applicationStandardEnrollmentClaims[c.AppID] = held
		m.mu.Unlock()
	}, func(id string) { memAutomaticObservationDue(m, id) })
}

func automaticObservationFairness(t *testing.T, s standardAutomaticObservationTestStore) {
	t.Helper()
	f := standardAutomaticSetup(t, s, false, true)
	apps := []App{automaticCreate(t, s, f, "observer-a"), automaticCreate(t, s, f, "observer-b"), automaticCreate(t, s, f, "observer-c")}
	for range apps {
		automaticRepair(t, s)
	}
	seen := map[string]bool{}
	for range apps {
		c, err := s.ClaimApplicationStandardObservation(t.Context(), "fair-observer")
		if err != nil || seen[c.AppID] {
			t.Fatal("pending evidence starved another application", err)
		}
		seen[c.AppID] = true
		if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ClaimApplicationStandardObservation(t.Context(), "same-pass"); !errors.Is(err, ErrNotFound) {
		t.Fatal("fairness cooldown missing", err)
	}
}

func TestMemApplicationStandardAutomaticObservationFairness(t *testing.T) {
	automaticObservationFairness(t, NewMemStore())
}

func automaticObservationNewRevision(t *testing.T, s standardAutomaticObservationTestStore) {
	t.Helper()
	f := standardAutomaticSetup(t, s, false, true)
	app := automaticCreate(t, s, f, "observer-new-revision")
	automaticRepair(t, s)
	old, err := s.ClaimApplicationStandardObservation(t.Context(), "old-revision-observer")
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.SetApplicationStandardLocalIntent(t.Context(), app.OrgID, app.AccountID, app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: old.DesiredRevision, Settings: json.RawMessage(`{"security_policy":"off"}`)})
	if err != nil || e.DesiredRevision != old.DesiredRevision+1 || e.State != "pending" {
		t.Fatal("new local intent was not queued", err)
	}
	if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("old revision observer changed new intent", err)
	}
	automaticRepair(t, s)
	// The new installed revision bypasses the previous attempt's cooldown.
	next, err := s.ClaimApplicationStandardObservation(t.Context(), "new-revision-observer")
	if err != nil || next.DesiredRevision != e.DesiredRevision || next.Generation <= old.Generation {
		t.Fatal("new revision waited on old observation metadata", err)
	}
	if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), next); err != nil {
		t.Fatal(err)
	}
}

func TestMemApplicationStandardAutomaticObservationNewRevision(t *testing.T) {
	automaticObservationNewRevision(t, NewMemStore())
}

func automaticObservationLateReview(t *testing.T, s standardAutomaticObservationTestStore) {
	t.Helper()
	f := standardAutomaticSetup(t, s, false, true)
	app := automaticCreate(t, s, f, "observer-late-review")
	automaticRepair(t, s)
	c, err := s.ClaimApplicationStandardObservation(t.Context(), "claimed-before-approval")
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.PublishApplicationStandardVersion(t.Context(), ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: "observer-late-standard", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"security_policy":{"mode":"default","value":"off"}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: v.StandardID, AdmissionVersion: v.Version, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatal("late review failed", err, p.Blockers)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), app.OrgID, app.AccountID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), c); !errors.Is(err, ErrApplicationStandardOperationInProgress) && !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("pre-approval observer bypassed new reviewed ownership", err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || e.ObservedRevision != 0 || e.ErrorCode != "" {
		t.Fatal("late observer changed reviewed adoption", err)
	}
}

func TestMemApplicationStandardAutomaticObservationLateReview(t *testing.T) {
	automaticObservationLateReview(t, NewMemStore())
}
