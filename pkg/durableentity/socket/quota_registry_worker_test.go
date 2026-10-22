package socket

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
)

func allowRegistryWorker(durableentity.ID) bool { return true }
func workerSweep(t *testing.T, q *Quotas) RegistryCleanupWorkerPage {
	t.Helper()
	p, err := q.SweepRegistryCleanups(t.Context(), "", allowRegistryWorker)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRegistryCleanupWorkerDiscoversOnlyStartedOperationsAndResumes(t *testing.T) {
	q, id, _ := registryCleanupFixture(t)
	if p := workerSweep(t, q); p.Attempted != 0 || p.Failed != 0 {
		t.Fatal(p)
	}
	if c, err := q.RegistryCleanupStatus(t.Context(), id); err != nil || c != nil {
		t.Fatal("worker started cleanup", c, err)
	}
	c := startRegistryCleanup(t, q, id)
	if p := workerSweep(t, q); p.Attempted != 0 {
		t.Fatal("worker skipped grace", p)
	}
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	if p, err := q.SweepRegistryCleanups(t.Context(), "", func(durableentity.ID) bool { return false }); err != nil || p.Attempted != 0 {
		t.Fatal("worker ignored app allowlist", p, err)
	}
	if p := workerSweep(t, q); p.Attempted != 1 || p.Failed != 0 {
		t.Fatal(p)
	}
	other := *q
	other.RegistryCache = nil // A restarted process needs only durable records.
	for i := 0; i < 50; i++ {
		c, err := other.RegistryCleanupStatus(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if c.Phase == "complete" {
			break
		}
		now = now.Add(RegistryCleanupWorkerPoll)
		if p := workerSweep(t, &other); p.Attempted != 1 || p.Failed != 0 {
			t.Fatal(p)
		}
		if i == 49 {
			t.Fatal("worker failed to finish")
		}
	}
	c, err := other.RegistryCleanupStatus(t.Context(), id)
	if err != nil || c.Phase != "complete" {
		t.Fatal(c, err)
	}
	status, err := other.RegistryCleanupWorkerStatus(t.Context(), id)
	if err != nil || status == nil || status.ObservedRevision != c.Revision || status.LastSuccessAt.IsZero() || status.LastError != "" || status.Failures != 0 || !status.LeaseUntil.IsZero() {
		t.Fatal(status, err)
	}
	if p := workerSweep(t, &other); p.Attempted != 0 {
		t.Fatal("completed operation attempted", p)
	}
	u, err := other.Inspect(t.Context(), id)
	if err != nil || u.EntityReservations != 1 || u.ReservedBytes != 1024 {
		t.Fatal("worker changed quota", u, err)
	}
}
func TestRegistryCleanupWorkerUncertainDeleteBackoffSurvivesRestart(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	b.loseDelete = true
	if p := workerSweep(t, q); p.Attempted != 1 || p.Failed != 1 {
		t.Fatal(p)
	}
	status, err := q.RegistryCleanupWorkerStatus(t.Context(), id)
	if err != nil || status.LastError != "uncertain" || status.Failures != 1 || !status.NextAttemptAt.Equal(now.Add(10*time.Second)) {
		t.Fatal(status, err)
	}
	root, err := q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || root.Revision != c.Revision {
		t.Fatal("unknown delete advanced checkpoint", root, err)
	}
	other := *q
	other.RegistryCache = nil
	now = status.NextAttemptAt.Add(-time.Nanosecond)
	if p := workerSweep(t, &other); p.Attempted != 0 {
		t.Fatal("persisted backoff ignored", p)
	}
	now = status.NextAttemptAt
	if p := workerSweep(t, &other); p.Attempted != 1 || p.Failed != 0 {
		t.Fatal(p)
	}
	status, err = other.RegistryCleanupWorkerStatus(t.Context(), id)
	if err != nil || status.LastError != "" || status.Failures != 0 || status.LastSuccessAt.IsZero() {
		t.Fatal(status, err)
	}
	if workerError(errors.New("provider error contains secret=do-not-expose")) != "provider" {
		t.Fatal("raw provider error exposed")
	}
}

type lostRegistryWorkerClaim struct {
	*registryCleanupBucket
	lose bool
}

func (s *lostRegistryWorkerClaim) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	v, err := s.registryCleanupBucket.Put(ctx, key, body, version)
	if err == nil && strings.HasPrefix(key, registryWorkerPrefix) && s.lose {
		s.lose = false
		return "", errors.New("lost private claim acknowledgement")
	}
	return v, err
}
func TestRegistryCleanupWorkerUnknownClaimDoesNoWorkAndExpires(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	q.Store = &lostRegistryWorkerClaim{registryCleanupBucket: b, lose: true}
	if p := workerSweep(t, q); p.Attempted != 0 || p.Failed != 1 || len(b.deletes) != 0 {
		t.Fatal("unknown claim authorized work", p, b.deletes)
	}
	status, err := q.RegistryCleanupWorkerStatus(t.Context(), id)
	if err != nil || status.LeaseUntil.IsZero() {
		t.Fatal(status, err)
	}
	restarted := *q
	restarted.Store = b
	restarted.RegistryCache = nil
	if p := workerSweep(t, &restarted); p.Attempted != 0 {
		t.Fatal("live claim ignored", p)
	}
	scope, _ := scopeFor(id)
	oldRecord, oldVersion, err := q.readRegistryWorker(t.Context(), scopeKey(scope))
	if err != nil {
		t.Fatal(err)
	}
	now = status.LeaseUntil
	if p := workerSweep(t, &restarted); p.Attempted != 1 || p.Failed != 0 {
		t.Fatal("expired claim did not resume", p)
	}
	if _, err := restarted.putRegistryWorker(t.Context(), oldRecord, oldVersion); !errors.Is(err, durableentity.ErrConflict) {
		t.Fatal("delayed worker overwrote replacement observation", err)
	}
}

type gatedRegistryWorkerClaim struct {
	*registryCleanupBucket
	once             sync.Once
	claimed, release chan struct{}
}

func (s *gatedRegistryWorkerClaim) Put(ctx context.Context, key string, body []byte, version string) (string, error) {
	v, err := s.registryCleanupBucket.Put(ctx, key, body, version)
	if err == nil && strings.HasPrefix(key, registryWorkerPrefix) {
		s.once.Do(func() {
			close(s.claimed)
			select {
			case <-s.release:
			case <-ctx.Done():
			}
		})
	}
	return v, err
}
func TestRegistryCleanupWorkerCompetingNodesRespectClaim(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	gate := &gatedRegistryWorkerClaim{registryCleanupBucket: b, claimed: make(chan struct{}), release: make(chan struct{})}
	q.Store = gate
	other := *q
	other.Store = b
	other.RegistryCache = nil
	done := make(chan RegistryCleanupWorkerPage, 1)
	errs := make(chan error, 1)
	go func() {
		p, err := q.SweepRegistryCleanups(t.Context(), "", allowRegistryWorker)
		done <- p
		errs <- err
	}()
	select {
	case <-gate.claimed:
	case <-time.After(time.Second):
		t.Fatal("worker did not claim")
	}
	if p := workerSweep(t, &other); p.Attempted != 0 {
		t.Fatal("second node ignored claim", p)
	}
	close(gate.release)
	select {
	case p := <-done:
		if p.Attempted != 1 || p.Failed != 0 {
			t.Fatal(p)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	status, err := other.RegistryCleanupStatus(t.Context(), id)
	if err != nil || status.Revision != c.Revision+1 {
		t.Fatal("page duplicated", status, err)
	}
}
func TestRegistryCleanupWorkerRejectsMalformedDiscoveryWithoutClaim(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	for _, page := range []durableentity.CleanupObjects{{Keys: []string{"outside/private"}}, {Keys: make([]string, registryWorkerPageSize+1)}, {NextCursor: "not-progress"}} {
		b.page = &page
		if _, err := q.SweepRegistryCleanups(t.Context(), "", allowRegistryWorker); !errors.Is(err, durableentity.ErrCorrupt) {
			t.Fatal(err)
		}
	}
	b.page = nil
	if len(b.deletes) != 0 {
		t.Fatal("bad discovery authorized deletion")
	}
	if status, err := q.RegistryCleanupWorkerStatus(t.Context(), id); err != nil || status != nil {
		t.Fatal("bad discovery created a claim", status, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := q.SweepRegistryCleanups(ctx, "", allowRegistryWorker); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled sweep continued", err)
	}
}

func TestRegistryCleanupWorkerInspectsLostCheckpointBeforeNextAttempt(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	q.Store = &registryCleanupLostRoot{registryLostRootStore: &registryLostRootStore{ObjectStore: b, lose: true}, CleanupStore: b}
	if p := workerSweep(t, q); p.Attempted != 1 || p.Failed != 1 {
		t.Fatal(p)
	}
	status, err := q.RegistryCleanupWorkerStatus(t.Context(), id)
	if err != nil || status.LastError != "uncertain" || status.ObservedRevision != c.Revision {
		t.Fatal(status, err)
	}
	current, err := q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || current.Revision != c.Revision+1 {
		t.Fatal("lost checkpoint not committed", current, err)
	}
	if p := workerSweep(t, q); p.Attempted != 0 {
		t.Fatal("uncertain checkpoint bypassed backoff", p)
	}
	now = status.NextAttemptAt
	restarted := *q
	restarted.Store = b
	restarted.RegistryCache = nil
	if p := workerSweep(t, &restarted); p.Attempted != 1 || p.Failed != 0 {
		t.Fatal("worker retried obsolete checkpoint", p)
	}
	current, err = restarted.RegistryCleanupStatus(t.Context(), id)
	if err != nil || current.Revision != c.Revision+2 {
		t.Fatal(current, err)
	}
}

func TestRegistryCleanupWorkerDiscoveryPaginationIsBounded(t *testing.T) {
	q, id, _ := registryCleanupFixture(t)
	// Inactive roots still consume discovery budget but never start operations.
	for _, tenant := range []string{"one", "two", "three", "four", "five", "six"} {
		fresh := id
		fresh.TenantID = tenant
		if err := q.AdmitInvocation(t.Context(), fresh); err != nil {
			t.Fatal(err)
		}
	}
	c := startRegistryCleanup(t, q, id)
	now := c.NotBefore
	q.Now = func() time.Time { return now }
	cursor := ""
	attempted := 0
	for i := 0; i < 2; i++ {
		page, err := q.SweepRegistryCleanups(t.Context(), cursor, allowRegistryWorker)
		if err != nil || page.Failed != 0 || page.Attempted > registryWorkerPageSize {
			t.Fatal(page, err)
		}
		attempted += page.Attempted
		cursor = page.NextCursor
		if i == 0 && cursor == "" {
			t.Fatal("unbounded discovery")
		}
	}
	if cursor != "" || attempted != 1 {
		t.Fatal("scope skipped or duplicated", cursor, attempted)
	}
	current, err := q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || current.Revision != c.Revision+1 {
		t.Fatal(current, err)
	}
}

func TestRegistryCleanupWorkerProbeTouchesOnlyOwnPrivateKey(t *testing.T) {
	q, id, b := registryCleanupFixture(t)
	c := startRegistryCleanup(t, q, id)
	if err := q.CheckRegistryCleanupWorker(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(b.deletes) != 1 || !strings.HasPrefix(b.deletes[0], registryWorkerPrefix+"probes/") {
		t.Fatal("probe touched scope data", b.deletes)
	}
	current, err := q.RegistryCleanupStatus(t.Context(), id)
	if err != nil || current.Revision != c.Revision {
		t.Fatal("probe stepped cleanup", current, err)
	}
}
