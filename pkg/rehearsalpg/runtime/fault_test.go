// adr: 20261018000200
package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

type dependencyCatalog struct {
	base            *memoryCatalog
	mu              sync.Mutex
	fault           rehearsalfault.Row
	lostActiveAck   bool
	lostRecoveryAck bool
}

func (c *dependencyCatalog) ReserveScenarioPostgres(ctx context.Context, r rehearsalpg.Resource) (rehearsalpg.Resource, error) {
	return c.base.ReserveScenarioPostgres(ctx, r)
}
func (c *dependencyCatalog) GetScenarioPostgres(ctx context.Context, a, r string) (rehearsalpg.Resource, error) {
	return c.base.GetScenarioPostgres(ctx, a, r)
}
func (c *dependencyCatalog) ScenarioPostgresForApp(ctx context.Context, a string) (rehearsalpg.Resource, error) {
	return c.base.ScenarioPostgresForApp(ctx, a)
}
func (c *dependencyCatalog) TransitionScenarioPostgres(ctx context.Context, r rehearsalpg.Resource, s string) (rehearsalpg.Resource, error) {
	return c.base.TransitionScenarioPostgres(ctx, r, s)
}
func (c *dependencyCatalog) ExpiredScenarioPostgres(ctx context.Context) ([]rehearsalpg.Resource, error) {
	return c.base.ExpiredScenarioPostgres(ctx)
}
func (c *dependencyCatalog) ScenarioPostgresFault(context.Context, string) (rehearsalfault.Row, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return rehearsalfault.Clone(c.fault), nil
}
func (c *dependencyCatalog) ScenarioPostgresFaultForRun(context.Context, string, string) (rehearsalfault.Row, error) {
	return c.ScenarioPostgresFault(context.Background(), "")
}
func (c *dependencyCatalog) PendingScenarioPostgresFaults(context.Context) ([]rehearsalfault.Row, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []rehearsalfault.Row{rehearsalfault.Clone(c.fault)}, nil
}
func (c *dependencyCatalog) TransitionScenarioPostgresFault(_ context.Context, r rehearsalfault.Row, expected string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fault.State != expected || !rehearsalfault.Transition(c.fault, r) {
		return ErrFixture
	}
	if r.State == "active" && c.lostActiveAck {
		c.lostActiveAck = false
		return ErrFixture
	}
	if r.State == "recovered" && c.lostRecoveryAck {
		c.lostRecoveryAck = false
		return ErrFixture
	}
	c.fault = rehearsalfault.Clone(r)
	return nil
}

type dependencyTestBackend struct {
	base                    faultBackend
	blocked                 bool
	cuts, recovers          int
	cutFails, recoveryFails bool
	beforeCut               func()
}

func (b *dependencyTestBackend) Identity() string { return b.base.Identity() }
func (b *dependencyTestBackend) Provision(ctx context.Context, r rehearsalpg.Resource, s, p string) ([]rehearsalpg.Snapshot, error) {
	return b.base.Provision(ctx, r, s, p)
}
func (b *dependencyTestBackend) FenceAndCapture(ctx context.Context, r rehearsalpg.Resource) ([]rehearsalpg.Snapshot, error) {
	return b.base.FenceAndCapture(ctx, r)
}
func (b *dependencyTestBackend) LockWait(ctx context.Context, r rehearsalpg.Resource) (int64, error) {
	return b.base.LockWait(ctx, r)
}
func (b *dependencyTestBackend) Drop(ctx context.Context, r rehearsalpg.Resource) error {
	b.blocked = false
	return b.base.Drop(ctx, r)
}
func (b *dependencyTestBackend) CutPostgres(ctx context.Context, _ rehearsalpg.Resource, authorize func(context.Context) error) (int64, error) {
	if b.beforeCut != nil {
		b.beforeCut()
	}
	if authorize(ctx) != nil {
		return 0, ErrFixture
	}
	b.blocked = true
	b.cuts++
	if b.cutFails {
		return 0, ErrFixture
	}
	return 2, nil
}
func (b *dependencyTestBackend) RecoverPostgres(context.Context, rehearsalpg.Resource) error {
	b.recovers++
	if b.recoveryFails {
		return ErrFixture
	}
	b.blocked = false
	return nil
}

func dependencyService() (*Service, *dependencyCatalog, *dependencyTestBackend) {
	b := &dependencyTestBackend{}
	c := &dependencyCatalog{base: &memoryCatalog{rows: map[string]rehearsalpg.Resource{}}}
	s := NewService(c, b)
	a, run := uuid.NewString(), strings.Repeat("a", 32)
	r := rehearsalpg.Resource{ID: uuid.NewString(), AccountID: a, RunID: run, AppID: uuid.NewString(), OwnerID: s.OwnerID, BackendSHA256: b.Identity(), Status: "ready", ExpiresAt: time.Now().Add(time.Minute)}
	c.base.rows[a+run] = r
	c.fault = rehearsalfault.Row{Receipt: rehearsalfault.Receipt{ID: uuid.NewString(), AccountID: a, RunID: run, AppID: uuid.NewString(), ResourceID: r.ID, DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), RecorderSessionID: uuid.NewString(), InvocationID: uuid.NewString(), CheckpointAttemptID: uuid.NewString(), OwnerID: s.OwnerID, BackendSHA256: b.Identity(), PlanSHA256: strings.Repeat("b", 64), Spec: rehearsalfault.Postgres{Workload: "worker", Checkpoint: "after-commit", UnavailableMS: 100, RecoverWithinMS: 5000}, State: "requested"}, CreatedAt: time.Now(), DeadlineAt: time.Now().Add(30 * time.Second)}
	return s, c, b
}
func TestPostgresFaultControllerCutsAndRestoresOnlyItsBoundResource(t *testing.T) {
	s, c, b := dependencyService()
	s.reconcilePostgresFaults(t.Context(), false)
	r, _ := c.ScenarioPostgresFault(t.Context(), c.fault.ID)
	if r.State != "active" || !b.blocked || b.cuts != 1 || b.recovers != 0 {
		t.Fatalf("no physical cut acknowledgement: %+v", r)
	}
	s.reconcilePostgresFaults(t.Context(), false)
	if b.recovers != 0 {
		t.Fatal("restored before the requested outage")
	}
	timer := time.NewTimer(110 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	s.reconcilePostgresFaults(t.Context(), false)
	r, _ = c.ScenarioPostgresFault(t.Context(), r.ID)
	if r.Validate() != nil || b.blocked || b.recovers != 1 {
		t.Fatalf("recovery not confirmed: %+v", r)
	}
}
func TestPostgresFaultFailuresRestoreAccessWithoutPassing(t *testing.T) {
	for _, mode := range []string{"cut response lost", "activation ack lost", "collector restart", "shutdown", "recovery failure", "recovery ack lost", "delete"} {
		t.Run(mode, func(t *testing.T) {
			s, c, b := dependencyService()
			b.cutFails = mode == "cut response lost"
			c.lostActiveAck = mode == "activation ack lost"
			s.reconcilePostgresFaults(t.Context(), false)
			if mode == "collector restart" {
				s = NewService(c, b)
				c.fault.DeadlineAt = time.Now().Add(-time.Second)
			}
			if mode == "recovery failure" {
				b.recoveryFails = true
			}
			if mode == "recovery ack lost" {
				c.lostRecoveryAck = true
			}
			if mode == "delete" {
				if err := s.Delete(t.Context(), c.fault.AccountID, c.fault.RunID); err != nil {
					t.Fatal(err)
				}
			} else {
				if mode != "shutdown" {
					timer := time.NewTimer(110 * time.Millisecond)
					<-timer.C
					timer.Stop()
				}
				s.reconcilePostgresFaults(t.Context(), mode == "shutdown")
			}
			b.recoveryFails = false
			if mode == "collector restart" {
				s.reconcilePostgresFaults(t.Context(), false)
			} else {
				s.reconcilePostgresFaults(t.Context(), true)
			}
			r, _ := c.ScenarioPostgresFault(t.Context(), c.fault.ID)
			if b.blocked || r.Validate() == nil {
				t.Fatalf("failure adopted or access left blocked: %+v", r)
			}
		})
	}
}
func TestPostgresFaultBackendMismatchNeverAcknowledgesAnotherCluster(t *testing.T) {
	s, c, b := dependencyService()
	c.fault.BackendSHA256 = strings.Repeat("d", 64)
	s.reconcilePostgresFaults(t.Context(), false)
	if b.cuts != 0 || b.recovers != 0 || c.fault.Recovered {
		t.Fatal("another backend changed or acknowledged")
	}
}

func TestPostgresFaultDelayedOldOwnerCannotCutAfterReplacementRecovery(t *testing.T) {
	s, c, b := dependencyService()
	b.beforeCut = func() {
		// The original owner has durably entered cutting but has not yet
		// acquired the physical database lock. Its replacement expires and
		// restores the obligation before the old command resumes.
		c.fault.DeadlineAt = time.Now().Add(-time.Second)
		replacement := NewService(c, b)
		replacement.reconcilePostgresFaults(t.Context(), false)
	}
	s.reconcilePostgresFaults(t.Context(), false)
	r, _ := c.ScenarioPostgresFault(t.Context(), c.fault.ID)
	if b.blocked || b.cuts != 0 || b.recovers != 1 || !r.Recovered || r.State != "failed" || r.Validate() == nil {
		t.Fatalf("stale owner reapplied an acknowledged recovery: %+v", r)
	}
}
