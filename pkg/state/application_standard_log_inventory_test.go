package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type standardLogInventoryTestStore interface {
	standardLocalIntentTestStore
	ApplicationStandardLogInventoryStore
	SetComputeNodeActive(context.Context, string, bool) error
}

func TestMemApplicationStandardLogInventory(t *testing.T) {
	standardLogInventoryLifecycle(t, NewMemStore())
}

type standardLogInventoryExceptionTestStore interface {
	standardLogInventoryTestStore
	ApplicationStandardExceptionStore
}

func TestMemApplicationStandardLogInventoryExceptionDeadline(t *testing.T) {
	standardLogInventoryExceptionDeadline(t, NewMemStore())
}

func standardLogInventoryExceptionDeadline(t *testing.T, s standardLogInventoryExceptionTestStore) {
	t.Helper()
	f := newStandardLocalIntentFixture(t.Context(), t, s)
	c := standardLogInventorySession(t, s, standardLogInventoryNode(t, s).ID)
	r := standardExceptionRequest(f)
	r.ExpiresAt = time.Now().UTC().Add(3 * time.Second)
	x, err := s.ApproveApplicationStandardException(t.Context(), f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(t.Context(), t, s, f)
	if f.enrollment.ExceptionExpiresAt == nil || !f.enrollment.ExceptionExpiresAt.Equal(x.ExpiresAt) {
		t.Fatalf("installed exception deadline differs: installed=%v approved=%v", f.enrollment.ExceptionExpiresAt, x.ExpiresAt)
	}
	i := standardLogInventoryCurrent(t, s, f, 1)
	standardLogInventoryWrite(t, s, c, i)
	waitStandardExceptionExpiry(t, x)
	if _, err := s.RecordApplicationStandardLogInventory(t.Context(), c, i); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("expired inventory accepted: %v", err)
	}
	rows, err := s.ListApplicationStandardLogInventories(t.Context(), i.OrgID, i.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("expired exception supplied current loaded evidence")
	}
}

func standardLogInventoryNode(t *testing.T, s standardLogInventoryTestStore) ComputeNode {
	t.Helper()
	n, err := s.CreateComputeNode(t.Context(), ComputeNode{Name: "logging-consumer-" + uuid.NewString(), TargetURL: "unix:///tmp/standards-log-consumer", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func standardLogInventorySession(t *testing.T, s standardLogInventoryTestStore, node string) ApplicationStandardLogConsumerSession {
	t.Helper()
	c, err := s.RegisterApplicationStandardLogConsumer(t.Context(), node, uuid.NewString())
	if err != nil || c.Generation < 1 {
		t.Fatalf("register consumer: %+v %v", c, err)
	}
	return c
}

func standardLogInventoryCurrent(t *testing.T, s standardLogInventoryTestStore, f standardLocalIntentFixture, count int) ApplicationStandardLogInventory {
	t.Helper()
	snap, err := s.LoadApplicationStandardLogConsumerSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range snap.Inventories {
		if !sameStandardUUID(i.AppID, f.app.ID) {
			continue
		}
		if len(i.Drains) != count || !validStandardLogInventory(i) {
			t.Fatalf("inventory: %+v", i)
		}
		for _, d := range snap.Drains {
			if sameStandardUUID(d.AppID, f.app.ID) {
				found := false
				for _, entry := range i.Drains {
					found = found || sameStandardUUID(d.ID, entry.DrainID) && ApplicationStandardLogDrainConfigHash(d) == entry.ConfigHash
				}
				if !found {
					t.Fatal("snapshot split drain configuration and inventory")
				}
			}
		}
		return i
	}
	t.Fatal("missing current inventory, including empty/removal projection")
	return ApplicationStandardLogInventory{}
}

func standardLogInventoryWrite(t *testing.T, s standardLogInventoryTestStore, c ApplicationStandardLogConsumerSession, i ApplicationStandardLogInventory) {
	t.Helper()
	before := time.Now().UTC().Add(-time.Second)
	o, err := s.RecordApplicationStandardLogInventory(t.Context(), c, i)
	if err != nil || !sameStandardLogInventory(o.ApplicationStandardLogInventory, i) || o.ApplicationStandardLogConsumerSession != c || o.ObservedAt.Before(before) || o.ObservedAt.After(time.Now()) {
		t.Fatalf("inventory observation: %+v %v", o, err)
	}
	rows, err := s.ListApplicationStandardLogInventories(t.Context(), i.OrgID, i.AppID)
	if err != nil || len(rows) == 0 {
		t.Fatalf("current node facts missing: %+v %v", rows, err)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), "example.com") || strings.Contains(string(encoded), "sealed-") {
		t.Fatal("inventory fact exposed sender configuration")
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), i.OrgID, i.AppID)
	if err != nil || e.ObservedRevision != 0 || e.State != "persisted" {
		t.Fatal("inventory advanced whole-application observation")
	}
}

func standardLogInventoryLifecycle(t *testing.T, s standardLogInventoryTestStore) {
	t.Helper()
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s) // No deployment, instance, or log event.
	node := standardLogInventoryNode(t, s)
	c := standardLogInventorySession(t, s, node.ID)
	i := standardLogInventoryCurrent(t, s, f, 1)
	standardLogInventoryWrite(t, s, c, i)
	standardLogInventoryRestart(t, s, &c, i)
	second := standardLogInventorySession(t, s, standardLogInventoryNode(t, s).ID)
	standardLogInventoryWrite(t, s, second, i)
	rows, err := s.ListApplicationStandardLogInventories(ctx, i.OrgID, i.AppID)
	if err != nil || len(rows) != 2 {
		t.Fatal("one node fact replaced another node")
	}
	if _, err := s.ListApplicationStandardLogInventories(ctx, uuid.NewString(), i.AppID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-organization inventory exposed")
	}
	standardLogInventoryNodeAvailability(t, s, c, i)
	standardLogInventoryChangeAndRemoval(t, s, &f, c, i)
}

func standardLogInventoryRestart(t *testing.T, s standardLogInventoryTestStore, c *ApplicationStandardLogConsumerSession, i ApplicationStandardLogInventory) {
	t.Helper()
	ctx := t.Context()
	retry, err := s.RegisterApplicationStandardLogConsumer(ctx, c.NodeID, c.SessionID)
	if err != nil || retry != *c {
		t.Fatal("registration retry changed generation")
	}
	next := standardLogInventorySession(t, s, c.NodeID)
	if next.Generation != c.Generation+1 {
		t.Fatal("restart reused predecessor's generation")
	}
	if _, err := s.RegisterApplicationStandardLogConsumer(ctx, c.NodeID, c.SessionID); !errors.Is(err, ErrApplicationStandardLogConsumerFenced) {
		t.Fatalf("old registration retry reclaimed consumer: %v", err)
	}
	if err := s.CheckApplicationStandardLogConsumer(ctx, *c); !errors.Is(err, ErrApplicationStandardLogConsumerFenced) {
		t.Fatal("old daemon remained current")
	}
	if _, err := s.RecordApplicationStandardLogInventory(ctx, *c, i); !errors.Is(err, ErrApplicationStandardLogConsumerFenced) {
		t.Fatal("late old-daemon acknowledgment accepted")
	}
	rows, err := s.ListApplicationStandardLogInventories(ctx, i.OrgID, i.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("restart inherited predecessor's observations")
	}
	*c = next
	standardLogInventoryWrite(t, s, *c, i)
}

func standardLogInventoryNodeAvailability(t *testing.T, s standardLogInventoryTestStore, c ApplicationStandardLogConsumerSession, i ApplicationStandardLogInventory) {
	t.Helper()
	ctx := t.Context()
	if err := s.SetComputeNodeActive(ctx, c.NodeID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckApplicationStandardLogConsumer(ctx, c); err != nil {
		t.Fatal("temporary unavailability changed session identity")
	}
	if _, err := s.RecordApplicationStandardLogInventory(ctx, c, i); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatalf("unavailable node accepted fact: %v", err)
	}
	rows, err := s.ListApplicationStandardLogInventories(ctx, i.OrgID, i.AppID)
	if err != nil || len(rows) != 1 || rows[0].NodeID == c.NodeID {
		t.Fatal("unavailable node supplied current evidence")
	}
	if err := s.SetComputeNodeActive(ctx, c.NodeID, true); err != nil {
		t.Fatal(err)
	}
	standardLogInventoryWrite(t, s, c, i)
}

func standardLogInventoryChangeAndRemoval(t *testing.T, s standardLogInventoryTestStore, f *standardLocalIntentFixture, c ApplicationStandardLogConsumerSession, old ApplicationStandardLogInventory) {
	t.Helper()
	ctx := t.Context()
	r := ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{}`), AdditionalLogDestinations: []string{f.extra.ID}}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordApplicationStandardLogInventory(ctx, c, old); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("pending revision accepted old inventory")
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, *f)
	i := standardLogInventoryCurrent(t, s, *f, 2)
	if _, err := s.RecordApplicationStandardLogInventory(ctx, c, old); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("missing addition accepted as complete")
	}
	standardLogInventoryWrite(t, s, c, i)
	f.enrollment = standardLogInventoryRemoveAssignment(t, s, *f)
	empty := standardLogInventoryCurrent(t, s, *f, 0)
	if _, err := s.RecordApplicationStandardLogInventory(ctx, c, i); !errors.Is(err, ErrApplicationStandardLogDeliveryStale) {
		t.Fatal("removed destinations accepted as current")
	}
	standardLogInventoryWrite(t, s, c, empty)
}

func standardLogInventoryRemoveAssignment(t *testing.T, s standardLogInventoryTestStore, f standardLocalIntentFixture) ApplicationStandardEnrollment {
	t.Helper()
	ctx := t.Context()
	// Clear the permitted extra first; removal must represent an actual zero set.
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	f.enrollment = standardLocalIntentMaterialize(ctx, t, s, f)
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, ApplicationStandardReviewRequest{AssignmentID: f.assignmentID, ExpectedRevision: 1, Scope: "organization", ScopeID: f.owner.PersonalOrg.ID, StandardID: f.version.StandardID, AdmissionVersion: 1, Active: false, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("removal review: %+v %v", p.Blockers, err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "logging-inventory-removal")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || o.State != "waiting" || len(o.Targets) != 1 || o.Targets[0].State != "persisted" {
		t.Fatalf("removal incorrectly completed: %+v %v", o, err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, p.OrgID, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestMemApplicationStandardLogInventoryFreshnessAndCancel(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	c := standardLogInventorySession(t, m, standardLogInventoryNode(t, m).ID)
	i := standardLogInventoryCurrent(t, m, f, 1)
	standardLogInventoryWrite(t, m, c, i)
	m.mu.Lock()
	for k, o := range m.applicationStandardLogInventories {
		o.ObservedAt = time.Now().Add(-time.Hour)
		m.applicationStandardLogInventories[k] = o
	}
	m.mu.Unlock()
	rows, err := m.ListApplicationStandardLogInventories(t.Context(), i.OrgID, i.AppID)
	if err != nil || len(rows) != 0 {
		t.Fatal("offline consumer supplied fresh evidence")
	}
	ctx, cancel := context.WithCancel(t.Context())
	entered, finished := make(chan struct{}), make(chan error, 1)
	m.mu.Lock()
	go func() { close(entered); _, err := m.RecordApplicationStandardLogInventory(ctx, c, i); finished <- err }()
	<-entered
	cancel()
	m.mu.Unlock()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("canceled storage wait published acknowledgment")
	}
}

func TestMemApplicationStandardLogInventoryNodeErasure(t *testing.T) {
	m := NewMemStore()
	f := newStandardLocalIntentFixture(t.Context(), t, m)
	c := standardLogInventorySession(t, m, standardLogInventoryNode(t, m).ID)
	i := standardLogInventoryCurrent(t, m, f, 1)
	standardLogInventoryWrite(t, m, c, i)
	c = standardLogInventorySession(t, m, c.NodeID)
	standardLogInventoryWrite(t, m, c, i)
	if err := m.DeleteComputeNode(t.Context(), c.NodeID); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckApplicationStandardLogConsumer(t.Context(), c); !errors.Is(err, ErrApplicationStandardLogConsumerFenced) {
		t.Fatal("erased node remained current")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.applicationStandardLogConsumers) != 0 || len(m.applicationStandardLogConsumerSessions) != 0 || len(m.applicationStandardLogInventories) != 0 {
		t.Fatal("erased node retained private consumer state")
	}
}
