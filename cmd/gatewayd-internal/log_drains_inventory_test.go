package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type inventoryGatewayTestStore interface {
	state.Store
	state.ApplicationStandardStore
	state.ApplicationStandardResourceStore
	state.ApplicationStandardReviewStore
	state.ApplicationStandardOperationStore
	state.ApplicationStandardEnrollmentStore
	state.ApplicationStandardMaterializationStore
	state.ApplicationStandardAutomaticMaterializationStore
	state.ApplicationStandardLocalIntentStore
	state.ApplicationStandardLogInventoryStore
	state.ApplicationStandardLogDeliveryStore
}

type inventoryGatewayFixture struct {
	s            inventoryGatewayTestStore
	owner        state.CreateAccountWithPersonalOrgResult
	app          state.App
	version      state.ApplicationStandardVersion
	assignmentID string
	company      state.ApplicationStandardLogDestination
}

func newInventoryGatewayFixture(t *testing.T, s inventoryGatewayTestStore) inventoryGatewayFixture {
	return newInventoryGatewayFixtureWithTarget(t, s, "https://logs.example.com")
}

func newInventoryGatewayFixtureWithTarget(t *testing.T, s inventoryGatewayTestStore, target string) inventoryGatewayFixture {
	t.Helper()
	ctx := t.Context()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "inventory-gateway@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	f := inventoryGatewayFixture{s: s, owner: owner}
	f.company, err = s.CreateApplicationStandardLogDestination(ctx, state.ApplicationStandardLogDestinationCreate{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Name: "company", Kind: "http_json", TargetURL: target, AuthHeaderSealed: []byte("good")})
	if err != nil {
		t.Fatal(err)
	}
	f.version, err = s.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "logging", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"log_destinations":{"mode":"mandatory","override":"extend","value":["` + f.company.ID + `"]}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	f.assignmentID = inventoryGatewayInitialAdmission(t, f)
	f.app, err = s.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "quiet-service", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	inventoryGatewayMaterialize(t, f)
	return f
}

func inventoryGatewayInitialAdmission(t *testing.T, f inventoryGatewayFixture) string {
	t.Helper()
	ctx := t.Context()
	p, err := f.s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, state.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("initial review: %+v %v", p.Blockers, err)
	}
	if _, err := f.s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := f.s.ClaimApplicationStandardOperation(ctx, "inventory-gateway-admission")
	if err != nil {
		t.Fatal(err)
	}
	o, err := f.s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || o.State != "completed" || len(o.Targets) != 0 {
		t.Fatal("empty scope did not checkpoint")
	}
	return p.Request.AssignmentID
}

func inventoryGatewayMaterialize(t *testing.T, f inventoryGatewayFixture) state.ApplicationStandardEnrollment {
	t.Helper()
	c, err := f.s.ClaimApplicationStandardEnrollment(t.Context(), "inventory-gateway-worker")
	if err != nil {
		t.Fatal(err)
	}
	e, err := f.s.MaterializeApplicationStandardEnrollment(t.Context(), c)
	if err != nil || e.State != "persisted" || e.ObservedRevision != 0 {
		t.Fatalf("materialization: %+v %v", e, err)
	}
	return e
}

type inventoryQuietLogs struct {
	gate    <-chan struct{}
	entered chan struct{}
}

func (q *inventoryQuietLogs) ScheddForApp(context.Context, string) (logStreamer, error) {
	return q, nil
}
func (q *inventoryQuietLogs) StreamAppLogs(ctx context.Context, _ string, _ int64, _ time.Time, _ bool, _ string, _ string, _ string) (scheddgrpc.LogStream, error) {
	return &inventoryQuietLogStream{ctx: ctx, gate: q.gate, entered: q.entered}, nil
}

type inventoryQuietLogStream struct {
	ctx     context.Context
	gate    <-chan struct{}
	entered chan struct{}
}

func (q *inventoryQuietLogStream) Recv() (scheddgrpc.LogFrame, error) {
	if q.entered != nil {
		select {
		case q.entered <- struct{}{}:
		default:
		}
	}
	<-q.ctx.Done()
	if q.gate != nil {
		<-q.gate
	}
	return scheddgrpc.LogFrame{}, q.ctx.Err()
}

func inventoryGatewayManager(t *testing.T, f inventoryGatewayFixture, q *inventoryQuietLogs) *appLogDrainManager {
	t.Helper()
	n, err := f.s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "inventory-gateway-" + uuid.NewString(), TargetURL: "unix:///tmp/inventory-gateway", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	m := newAppLogDrainManager(f.s, q, func([]byte) (string, error) { return "Authorization: Bearer company", nil }, nil, nil)
	m.spoolRoot = t.TempDir()
	m.standardNode = newLocalNodeID(f.s, n.Name)
	if err := m.acquireLogSpoolLease(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stopAllLogWorkersJoined(); _ = m.spoolLease.Unlock() })
	return m
}

func inventoryGatewayFacts(t *testing.T, f inventoryGatewayFixture, count, drains int) {
	t.Helper()
	ctx := t.Context()
	rows, err := f.s.ListApplicationStandardLogInventories(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(rows) != count || count > 0 && len(rows[0].Drains) != drains {
		t.Fatalf("inventory facts: %+v %v", rows, err)
	}
	delivery, err := f.s.ListApplicationStandardLogDeliveries(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || len(delivery) != 0 {
		t.Fatal("quiet-service load fabricated provider delivery")
	}
	e, err := f.s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || e.ObservedRevision != 0 || e.State != "persisted" {
		t.Fatal("logging fact advanced whole-application observation")
	}
}

func TestStandardLogInventoryQuietService(t *testing.T) {
	standardLogInventoryGatewayQuiet(t, state.NewMemStore())
}

func standardLogInventoryGatewayQuiet(t *testing.T, s inventoryGatewayTestStore) {
	t.Helper()
	f := newInventoryGatewayFixture(t, s)
	m := inventoryGatewayManager(t, f, &inventoryQuietLogs{})
	m.reconcile(t.Context())
	m.flushStandardLogInventories(t.Context())
	inventoryGatewayFacts(t, f, 1, 1)
	if _, err := s.RegisterApplicationStandardLogConsumer(t.Context(), m.standardSession.NodeID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	m.flushStandardLogInventories(t.Context())
	if !m.standardLogConsumerFenced() {
		t.Fatal("superseded manager did not stop observation")
	}
	inventoryGatewayFacts(t, f, 0, 0)
}

func TestStandardLogInventoryPartialStartup(t *testing.T) {
	f := newInventoryGatewayFixture(t, state.NewMemStore())
	e, err := f.s.GetApplicationStandardEnrollment(t.Context(), f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := f.s.CreateApplicationStandardLogDestination(t.Context(), state.ApplicationStandardLogDestinationCreate{OrgID: f.owner.PersonalOrg.ID, ActorID: f.owner.Account.ID, Name: "extra", Kind: "http_json", TargetURL: "https://extra.example.com", AuthHeaderSealed: []byte("bad")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetApplicationStandardLocalIntent(t.Context(), f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, state.ApplicationStandardLocalIntentRequest{ExpectedRevision: e.DesiredRevision, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{extra.ID}}); err != nil {
		t.Fatal(err)
	}
	inventoryGatewayMaterialize(t, f)
	m := inventoryGatewayManager(t, f, &inventoryQuietLogs{})
	m.unseal = func(b []byte) (string, error) {
		if string(b) == "bad" {
			return "", errors.New("key unavailable")
		}
		return "Authorization: Bearer company", nil
	}
	m.reconcile(t.Context())
	m.flushStandardLogInventories(t.Context())
	inventoryGatewayFacts(t, f, 0, 0)
	m.unseal = func([]byte) (string, error) { return "Authorization: Bearer company", nil }
	m.reconcile(t.Context())
	m.flushStandardLogInventories(t.Context())
	inventoryGatewayFacts(t, f, 1, 2)
}

func TestStandardLogInventoryRemovalWaitsForWorkers(t *testing.T) {
	standardLogInventoryGatewayRemoval(t, state.NewMemStore())
}

func standardLogInventoryGatewayRemoval(t *testing.T, s inventoryGatewayTestStore) {
	t.Helper()
	f := newInventoryGatewayFixture(t, s)
	gate := make(chan struct{})
	var release sync.Once
	q := &inventoryQuietLogs{gate: gate, entered: make(chan struct{}, 1)}
	m := inventoryGatewayManager(t, f, q)
	t.Cleanup(func() { release.Do(func() { close(gate) }) })
	m.reconcile(t.Context())
	select {
	case <-q.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("log stream did not start")
	}
	m.flushStandardLogInventories(t.Context())
	inventoryGatewayFacts(t, f, 1, 1)
	var old *appLogDrainWorker
	for _, w := range m.workers {
		old = w
	}
	inventoryGatewayRemoveStandard(t, f)
	m.reconcile(t.Context())
	m.flushStandardLogInventories(t.Context())
	inventoryGatewayFacts(t, f, 0, 0)
	if old == nil || !old.stopping {
		t.Fatal("removed worker was not retiring")
	}
	release.Do(func() { close(gate) })
	select {
	case <-old.done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not join after upstream cancellation")
	}
	m.reconcile(t.Context())
	m.flushStandardLogInventories(t.Context())
	inventoryGatewayFacts(t, f, 1, 0)
}

func inventoryGatewayRemoveStandard(t *testing.T, f inventoryGatewayFixture) {
	t.Helper()
	ctx := t.Context()
	p, err := f.s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, state.ApplicationStandardReviewRequest{AssignmentID: f.assignmentID, ExpectedRevision: 1, Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: false, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("removal review: %+v %v", p.Blockers, err)
	}
	if _, err := f.s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := f.s.ClaimApplicationStandardOperation(ctx, "inventory-gateway-removal")
	if err != nil {
		t.Fatal(err)
	}
	o, err := f.s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || o.State != "waiting" {
		t.Fatal("removal fabricated application convergence")
	}
}

func TestStandardLogInventoryUnknownNodeRemainsPending(t *testing.T) {
	f := newInventoryGatewayFixture(t, state.NewMemStore())
	m := inventoryGatewayManager(t, f, &inventoryQuietLogs{})
	m.standardNode = newLocalNodeID(f.s, "")
	m.reconcile(t.Context())
	m.flushStandardLogInventories(t.Context())
	inventoryGatewayFacts(t, f, 0, 0)
}

func TestStandardLogManagerLockSurvivesWorkerShutdown(t *testing.T) {
	m := newAppLogDrainManager(state.NewMemStore(), nil, nil, nil, nil)
	m.spoolRoot = t.TempDir()
	if err := m.acquireLogSpoolLease(); err != nil {
		t.Fatal(err)
	}
	defer m.spoolLease.Unlock()
	next := newAppLogDrainManager(state.NewMemStore(), nil, nil, nil, nil)
	next.spoolRoot = m.spoolRoot
	stopping, exited := make(chan struct{}), make(chan struct{})
	m.workers["old"] = &appLogDrainWorker{cancel: func() { close(stopping) }, done: exited}
	finished := make(chan struct{})
	go func() { m.stopAllLogWorkersJoined(); m.spoolLease.Unlock(); close(finished) }()
	<-stopping
	if err := next.acquireLogSpoolLease(); err == nil {
		t.Fatal("replacement acquired spool while worker was still exiting")
	}
	close(exited)
	<-finished
	if err := next.acquireLogSpoolLease(); err != nil {
		t.Fatal(err)
	}
	defer next.spoolLease.Unlock()
}
