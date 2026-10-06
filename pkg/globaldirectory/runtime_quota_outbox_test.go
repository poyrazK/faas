package globaldirectory

// adr: 647
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func outboxFixture(t *testing.T, f quotaFixture) (RuntimeQuotaAuthority, QuotaDeliveryRequest) {
	t.Helper()
	j, c := consentFixture(t, f)
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := runtimequota.NewSigner(c.IssuerID, private)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: c.IssuerID, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.PrepareRuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, c); err != nil {
		t.Fatal(err)
	}
	if _, err = j.ObserveRuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, c.ID, state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.quota.ReserveRuntimeQuota(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
	return RuntimeQuotaAuthority{Store: f.quota, Signer: signer, Directories: trust}, QuotaDeliveryRequest{OwnerID: f.owner.ID, Slug: f.request.Slug, ID: uuid.NewString(), AllocationID: f.request.AllocationID, Region: c.Binding.Region, Purpose: QuotaDeliveryGrant, ConsentID: c.ID, IssuerID: c.IssuerID}
}

func exerciseQuotaOutbox(t *testing.T, f quotaFixture) {
	t.Helper()
	q, req := outboxFixture(t, f)
	s := f.store.(QuotaDeliveryStore)
	signFailure := errors.New("signing failed")
	if _, err := s.QueueQuotaDelivery(t.Context(), req, func(RuntimeQuotaAllocation, RuntimeQuotaTarget) (runtimequota.Delivery, error) {
		return runtimequota.Delivery{}, signFailure
	}); !errors.Is(err, signFailure) {
		t.Fatal("signing failure lost", err)
	}
	a, err := f.quota.RuntimeQuota(t.Context(), req.OwnerID, req.Slug, req.AllocationID)
	if err != nil || a.Targets[0].IssuedAt != nil {
		t.Fatal("failed signing left issued marker", err)
	}
	if _, err = s.QuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed signing left job", err)
	}
	first, err := q.QueueDelivery(t.Context(), req)
	if err != nil || first.Status != QuotaDeliveryPending || q.verifyDeliveryJob(first) != nil {
		t.Fatal("queue", first.Status, err)
	}
	if got, err := s.QueueQuotaDelivery(t.Context(), req, func(RuntimeQuotaAllocation, RuntimeQuotaTarget) (runtimequota.Delivery, error) {
		t.Error("retry resigned original")
		return runtimequota.Delivery{}, signFailure
	}); err != nil || got.Delivery != first.Delivery || !got.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("retry changed original", err)
	}
	oldAnchor, err := q.Signer.PublicAnchor(runtimequota.Directory)
	if err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	q.Signer, err = runtimequota.NewSigner(req.IssuerID, private)
	if err != nil {
		t.Fatal(err)
	}
	q.Directories, err = runtimequota.NewTrust([]runtimequota.Anchor{oldAnchor, {Role: runtimequota.Directory, IssuerID: req.IssuerID, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := q.QueueDelivery(t.Context(), req); err != nil || got.Delivery != first.Delivery || q.verifyDeliveryJob(got) != nil {
		t.Fatal("key rotation replaced original", err)
	}
	wrong := req
	wrong.ConsentID = uuid.NewString()
	if _, err = q.QueueDelivery(t.Context(), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("consent source replaced", err)
	}
	wrong = req
	wrong.ID = uuid.NewString()
	if _, err = q.QueueDelivery(t.Context(), wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("second job adopted same target", err)
	}
	claims, err := s.ClaimQuotaDeliveries(t.Context())
	if err != nil || len(claims) != 1 || claims[0].Attempts != 1 || claims[0].ClaimID == "" {
		t.Fatal("claim", claims, err)
	}
	if err = s.CheckQuotaDeliveryDispatch(t.Context(), claims[0]); err != nil {
		t.Fatal("dispatch", err)
	}
	stale := claims[0]
	stale.ClaimID = uuid.NewString()
	if err = s.FinishQuotaDelivery(t.Context(), stale, QuotaDeliveryPending, "uncertain", nil); !errors.Is(err, ErrQuotaDeliveryClaim) {
		t.Fatal("replacement claim finished", err)
	}
	if err = s.FinishQuotaDelivery(t.Context(), claims[0], QuotaDeliveryPending, "uncertain", nil); err != nil {
		t.Fatal(err)
	}
	if claims, err = s.ClaimQuotaDeliveries(t.Context()); err != nil || len(claims) != 0 {
		t.Fatal("backoff ignored", err)
	}
	if _, err = f.store.(RuntimeConsentStore).BeginRevokeRuntimeConsent(t.Context(), req.OwnerID, req.Slug, req.ConsentID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RetryQuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID); !errors.Is(err, ErrRuntimeConsentUnavailable) {
		t.Fatal("revocation allowed retry", err)
	}
	if _, err = f.quota.RetireRuntimeQuota(t.Context(), req.OwnerID, f.actor.ID, req.Slug, req.AllocationID); err != nil {
		t.Fatal(err)
	}
	req.ID, req.Purpose, req.ConsentID = uuid.NewString(), QuotaDeliveryRetirement, ""
	retirement, err := q.QueueDelivery(t.Context(), req)
	if err != nil || q.verifyDeliveryJob(retirement) != nil {
		t.Fatal("retirement depends on consent", err)
	}
	if err = f.store.DetachTarget(t.Context(), req.OwnerID, req.Slug, req.Region); err != nil {
		t.Fatal(err)
	}
	claims, err = s.ClaimQuotaDeliveries(t.Context())
	if err != nil || len(claims) != 1 || claims[0].Purpose != QuotaDeliveryRetirement || s.CheckQuotaDeliveryDispatch(t.Context(), claims[0]) != nil {
		t.Fatal("detached retirement", err)
	}
	ack := runtimequota.DeliveryReceipt{Retirement: &retirement.Delivery.Authority}
	if err = s.FinishQuotaDelivery(t.Context(), claims[0], QuotaDeliveryAccepted, "", &ack); err != nil {
		t.Fatal(err)
	}
	got, err := s.QuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID)
	if err != nil || got.AcceptedAt == nil || got.Status != QuotaDeliveryAccepted {
		t.Fatal("receipt missing", err)
	}
	a, err = f.quota.RuntimeQuota(t.Context(), req.OwnerID, req.Slug, req.AllocationID)
	if err != nil || a.Targets[0].ReleasedAt != nil {
		t.Fatal("delivery receipt released runtime capacity", err)
	}
}

func TestQuotaOutboxMemoryContract(t *testing.T) {
	exerciseQuotaOutbox(t, memoryQuotaFixture(t, false, 1))
}

func TestQuotaOutboxMemoryLegacy(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	q, req := outboxFixture(t, f)
	if _, err := f.quota.IssueRuntimeQuotaTarget(t.Context(), req.OwnerID, req.Slug, req.AllocationID, req.Region); err != nil {
		t.Fatal(err)
	}
	if _, err := q.QueueDelivery(t.Context(), req); !errors.Is(err, ErrQuotaDeliveryLegacy) {
		t.Fatal("legacy authority adopted", err)
	}
}

func exerciseQuotaOutboxSigningExpiry(t *testing.T, f quotaFixture) {
	t.Helper()
	q, req := outboxFixture(t, f)
	s := f.store.(QuotaDeliveryStore)
	j := f.store.(RuntimeConsentStore)
	original, err := j.RuntimeConsent(t.Context(), req.OwnerID, req.Slug, req.ConsentID)
	if err != nil {
		t.Fatal(err)
	}
	c := original.Consent
	c.ID = uuid.NewString()
	c.ExpiresAtMicros = time.Now().Add(time.Second).UnixMicro()
	if _, err = j.PrepareRuntimeConsent(t.Context(), req.OwnerID, req.Slug, c); err != nil {
		t.Fatal(err)
	}
	if _, err = j.ObserveRuntimeConsent(t.Context(), req.OwnerID, req.Slug, c.ID, state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}); err != nil {
		t.Fatal(err)
	}
	req.ConsentID = c.ID
	_, err = s.QueueQuotaDelivery(t.Context(), req, func(a RuntimeQuotaAllocation, target RuntimeQuotaTarget) (runtimequota.Delivery, error) {
		g, err := quotaWireGrant(req.IssuerID, a, target)
		if err != nil {
			return runtimequota.Delivery{}, err
		}
		e, err := q.Signer.SignGrant(g)
		time.Sleep(max(0, time.Until(time.UnixMicro(c.ExpiresAtMicros))) + 20*time.Millisecond)
		return runtimequota.Delivery{Binding: g.Binding, Authority: e, ConsentID: c.ID}, err
	})
	if !errors.Is(err, ErrRuntimeConsentUnavailable) {
		t.Fatal("expired signing committed", err)
	}
	a, err := f.quota.RuntimeQuota(t.Context(), req.OwnerID, req.Slug, req.AllocationID)
	if err != nil || a.Targets[0].IssuedAt != nil {
		t.Fatal("expiry left issued marker", err)
	}
	if _, err = s.QuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expiry left job", err)
	}
}

func TestQuotaOutboxMemorySigningExpiry(t *testing.T) {
	exerciseQuotaOutboxSigningExpiry(t, memoryQuotaFixture(t, false, 1))
}

func TestQuotaOutboxMemoryFairnessAndRetryBound(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	q, req := outboxFixture(t, f)
	s := f.store.(*MemoryStore)
	original, err := q.QueueDelivery(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	owner2, owner3 := uuid.NewString(), uuid.NewString()
	for i, owner := range []string{req.OwnerID, owner2, owner3} {
		j := original
		j.ID = uuid.NewString()
		j.OwnerID = owner
		j.NextAttemptAt = j.NextAttemptAt.Add(time.Duration(i) * time.Millisecond)
		s.runtimeQuotaDeliveries[j.ID] = j
	}
	s.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	first, err := s.ClaimQuotaDeliveries(t.Context())
	if err != nil || len(first) != api.GlobalQuotaDeliveryBatch || first[0].OwnerID == first[1].OwnerID {
		t.Fatal("owner monopolized batch", err)
	}
	second, err := s.ClaimQuotaDeliveries(t.Context())
	if err != nil || len(second) == 0 {
		t.Fatal("owner starved", err)
	}
	for _, c := range first {
		if c.OwnerID == second[0].OwnerID {
			t.Fatal("already served owner outranked waiting owner")
		}
	}
	// Blocked retries use the same live-work bound as fresh insertions.
	s.mu.Lock()
	j := original
	j.Status = QuotaDeliveryBlocked
	j.LastCode = "trust"
	s.runtimeQuotaDeliveries[req.ID] = j
	for range api.GlobalQuotaDeliveryJobsPerAppMax {
		pending := original
		pending.ID = uuid.NewString()
		s.runtimeQuotaDeliveries[pending.ID] = pending
	}
	s.mu.Unlock()
	var bound *QuotaDeliveryLimitError
	if _, err = s.RetryQuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID); !errors.As(err, &bound) {
		t.Fatal("retry exceeded bound", err)
	}
}

func TestQuotaOutboxMemoryClaimsAndOwnerPause(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	q, req := outboxFixture(t, f)
	s := f.store.(*MemoryStore)
	if _, err := q.QueueDelivery(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	counts := make(chan int, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claims, err := s.ClaimQuotaDeliveries(t.Context())
			if err != nil {
				t.Error(err)
			}
			counts <- len(claims)
		}()
	}
	wg.Wait()
	close(counts)
	n := 0
	for count := range counts {
		n += count
	}
	if n != 1 {
		t.Fatal("duplicate claim", n)
	}
	s.mu.Lock()
	old := copyQuotaDelivery(s.runtimeQuotaDeliveries[req.ID])
	expired := time.Now().Add(-time.Second)
	j := old
	j.ClaimUntil = &expired
	s.runtimeQuotaDeliveries[req.ID] = j
	s.mu.Unlock()
	claims, err := s.ClaimQuotaDeliveries(t.Context())
	if err != nil || len(claims) != 1 || claims[0].ClaimID == old.ClaimID || claims[0].Attempts != 2 {
		t.Fatal("reclaim", err)
	}
	if err = s.FinishQuotaDelivery(t.Context(), old, QuotaDeliveryPending, "backend", nil); !errors.Is(err, ErrQuotaDeliveryClaim) {
		t.Fatal("old worker finished", err)
	}
	if err = f.authority.UpdateAccountStatus(t.Context(), f.actor.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	w := QuotaDeliveryWorker{Store: s, Authority: q}
	if err = w.deliver(t.Context(), claims[0]); !errors.Is(err, ErrOwnerInactive) {
		t.Fatal("paused owner dispatched", err)
	}
	got, err := s.QuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID)
	if err != nil || got.Status != QuotaDeliveryBlocked || got.LastCode != "owner" {
		t.Fatal("owner outcome", err)
	}
	if err = f.authority.UpdateAccountStatus(t.Context(), f.actor.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if got, err = s.RetryQuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID); err != nil || got.Status != QuotaDeliveryPending || got.Delivery != old.Delivery {
		t.Fatal("owner resume replaced authority", err)
	}
}

func TestQuotaOutboxWorkerLostReplyAndRestart(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	var bodies [][]byte
	var signer runtimequota.Signer
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("worker sent credential")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		var d runtimequota.Delivery
		if json.Unmarshal(body, &d) != nil {
			t.Error("invalid delivery")
		}
		if len(bodies) == 1 {
			http.Error(w, "lost response", http.StatusServiceUnavailable)
			return
		}
		if signer.IssuerID() == "" {
			t.Error("missing authority")
		}
		_ = json.NewEncoder(w).Encode(runtimequota.DeliveryReceipt{Grant: &d.Authority})
	}))
	defer srv.Close()
	cell, err := f.store.CellByRegion(t.Context(), "eu-fsn")
	if err != nil {
		t.Fatal(err)
	}
	cell.APIBase = srv.URL
	cell, err = f.store.PutCell(t.Context(), cell)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := f.store.ListTargets(t.Context(), f.owner.ID, f.request.Slug)
	if err != nil {
		t.Fatal(err)
	}
	b := targets[0].Binding
	b.CellGeneration = cell.Generation
	if _, err = f.store.AttachTarget(t.Context(), f.owner.ID, f.request.Slug, b); err != nil {
		t.Fatal(err)
	}
	q, req := outboxFixture(t, f)
	signer = q.Signer
	s := f.store.(QuotaDeliveryStore)
	first, err := q.QueueDelivery(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	w := QuotaDeliveryWorker{Store: s, Authority: q, Verifier: Verifier{Client: srv.Client()}}
	if err = w.Once(t.Context()); !errors.Is(err, ErrVerification) {
		t.Fatal("lost response", err)
	}
	if _, err = s.RetryQuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID); err != nil {
		t.Fatal(err)
	}
	// A new worker knows only durable state; no request credential is available.
	w = QuotaDeliveryWorker{Store: s, Authority: q, Verifier: Verifier{Client: srv.Client()}}
	if err = w.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := s.QuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID)
	if err != nil || got.Status != QuotaDeliveryAccepted || len(bodies) != 2 || string(bodies[0]) != string(bodies[1]) || got.Delivery != first.Delivery {
		t.Fatal("restart changed original", err)
	}
	if _, err = s.RetryQuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.Once(t.Context()); err != nil || len(bodies) != 2 {
		t.Fatal("accepted work redelivered", err)
	}
}

func TestQuotaOutboxWorkerCancellationRetainsLease(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	q, req := outboxFixture(t, f)
	s := f.store.(QuotaDeliveryStore)
	if _, err := q.QueueDelivery(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	claims, err := s.ClaimQuotaDeliveries(t.Context())
	if err != nil || len(claims) != 1 {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := QuotaDeliveryWorker{Store: s, Authority: q}
	if err = w.deliver(ctx, claims[0]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	got, err := s.QuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID)
	if err != nil || got.Status != QuotaDeliveryLeased || got.Receipt != nil {
		t.Fatal("cancelled worker acknowledged", err)
	}
}

// Observe the first cancellation check before the blocked mutation reaches mu.
// Capturing its result before signaling makes the cancellation schedule exact.
type quotaFinishCheckContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *quotaFinishCheckContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestQuotaOutboxMemoryCancellationDuringFinishWait(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	q, req := outboxFixture(t, f)
	s := f.store.(*MemoryStore)
	if _, err := q.QueueDelivery(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	claims, err := s.ClaimQuotaDeliveries(t.Context())
	if err != nil || len(claims) != 1 {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	probe := &quotaFinishCheckContext{Context: ctx, checked: make(chan struct{})}
	s.mu.Lock()
	result := make(chan error, 1)
	go func() { result <- s.FinishQuotaDelivery(probe, claims[0], QuotaDeliveryPending, "uncertain", nil) }()
	<-probe.checked
	cancel()
	s.mu.Unlock()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled finish mutated job", err)
	}
	j, err := s.QuotaDelivery(t.Context(), req.OwnerID, req.Slug, req.ID)
	if err != nil || j.Status != QuotaDeliveryLeased || j.LastCode != "" || j.Receipt != nil {
		t.Fatal("cancelled wait acknowledged work", err)
	}
}
