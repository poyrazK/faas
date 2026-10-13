package state

// adr: 802
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type regionalUsageFixture struct {
	store       Store
	quota       regionalQuotaFixture
	cell        runtimequota.Signer
	cells, dirs runtimequota.Trust
	usage       runtimequota.RuntimeUsage
	now         time.Time
}

func newRegionalUsageFixture(t *testing.T, s interface {
	Store
	RegionalRuntimeQuotaStore
}, a Account, cellID string) regionalUsageFixture {
	t.Helper()
	q := newRegionalQuotaFixture(t, s, s, a, cellID)
	minute := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	q.grant.IssuedAtMicros = minute.UnixMicro()
	if _, err := q.authority.AcceptGrant(t.Context(), q.grantEnvelope(t, q.grant)); err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cell, err := runtimequota.NewSigner(cellID, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := cell.PublicAnchor(runtimequota.Cell)
	if err != nil {
		t.Fatal(err)
	}
	da, err := q.signer.PublicAnchor(runtimequota.Directory)
	if err != nil {
		t.Fatal(err)
	}
	cells, err := runtimequota.NewTrust([]runtimequota.Anchor{ca})
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := runtimequota.NewTrust([]runtimequota.Anchor{da})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("fixture complete original measurements"))
	u := runtimequota.RuntimeUsage{Grant: q.grant, MinuteMicros: minute.UnixMicro(), MBSeconds: 520 * 17, SourceCount: 1, SourceDigest: hex.EncodeToString(digest[:]), ObservedAtMicros: minute.Add(time.Minute).UnixMicro()}
	return regionalUsageFixture{s, q, cell, cells, dirs, u, time.Now().UTC()}
}

func (f regionalUsageFixture) envelope(t *testing.T, u runtimequota.RuntimeUsage) runtimequota.Envelope {
	t.Helper()
	e, err := f.cell.SignRuntimeUsage(u)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func runRegionalUsageOutboxContract(t *testing.T, open func(*testing.T) regionalUsageFixture) {
	t.Helper()
	t.Run("concurrent-first-envelope-and-ack", func(t *testing.T) {
		f := open(t)
		e := f.envelope(t, f.usage)
		results := make(chan RegionalRuntimeUsageOutboxItem, 6)
		errs := make(chan error, 6)
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := QueueRegionalRuntimeUsage(t.Context(), f.store, f.cells, e, f.now)
				results <- r
				errs <- err
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var first RegionalRuntimeUsageOutboxItem
		for r := range results {
			if first.ID == 0 {
				first = r
			}
			if !reflect.DeepEqual(r, first) {
				t.Fatal("concurrent queue changed first envelope", r, first)
			}
		}
		if first.Envelope != e || first.Usage != f.usage || first.Ack != nil {
			t.Fatal(first)
		}
		pending, err := f.store.ListPendingRegionalRuntimeUsage(t.Context(), "", 0, 1)
		if err != nil || len(pending) != 1 || !reflect.DeepEqual(pending[0], first) {
			t.Fatal(pending, err)
		}
		ack := runtimequota.UsageImportAck{Usage: f.usage, ImportedAtMicros: f.now.UnixMicro()}
		signed, err := f.quota.signer.SignUsageImportAck(ack)
		if err != nil {
			t.Fatal(err)
		}
		imported, err := AcknowledgeRegionalRuntimeUsage(t.Context(), f.store, f.dirs, signed, f.now)
		if err != nil || imported.Ack == nil || *imported.Ack != ack || imported.Envelope != e {
			t.Fatal(imported, err)
		}
		again, err := AcknowledgeRegionalRuntimeUsage(t.Context(), f.store, f.dirs, signed, f.now)
		if err != nil || !reflect.DeepEqual(again, imported) {
			t.Fatal(again, err)
		}
		pending, err = f.store.ListPendingRegionalRuntimeUsage(t.Context(), "", 0, 1)
		if err != nil || len(pending) != 0 {
			t.Fatal(pending, err)
		}
		*imported.Ack = runtimequota.UsageImportAck{}
		*imported.AckEnvelope = runtimequota.Envelope{}
		got, err := f.store.RegionalRuntimeUsageOutbox(t.Context(), f.usage.Grant.Binding.RegionalAppID, f.usage.Grant.AllocationID, time.UnixMicro(f.usage.MinuteMicros))
		if err != nil || !reflect.DeepEqual(got, again) {
			t.Fatal("returned receipt aliases retained evidence", got, err)
		}
	})
	t.Run("rotation-collection-and-conflicts", func(t *testing.T) {
		f := open(t)
		ctx := t.Context()
		first, err := QueueRegionalRuntimeUsage(ctx, f.store, f.cells, f.envelope(t, f.usage), f.now)
		if err != nil {
			t.Fatal(err)
		}
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		rotated, err := runtimequota.NewSigner(f.cell.IssuerID(), key)
		if err != nil {
			t.Fatal(err)
		}
		anchor, err := rotated.PublicAnchor(runtimequota.Cell)
		if err != nil {
			t.Fatal(err)
		}
		trust, err := runtimequota.NewTrust([]runtimequota.Anchor{anchor})
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := rotated.SignRuntimeUsage(f.usage)
		if err != nil {
			t.Fatal(err)
		}
		if r, err := QueueRegionalRuntimeUsage(ctx, f.store, trust, envelope, f.now); err != nil || !reflect.DeepEqual(r, first) {
			t.Fatal(r, err)
		}
		for name, mutate := range map[string]func(*runtimequota.RuntimeUsage){
			"quantity":       func(u *runtimequota.RuntimeUsage) { u.MBSeconds++ },
			"digest":         func(u *runtimequota.RuntimeUsage) { u.SourceDigest = hex.EncodeToString(make([]byte, 32)) },
			"observation":    func(u *runtimequota.RuntimeUsage) { u.ObservedAtMicros++ },
			"original-owner": func(u *runtimequota.RuntimeUsage) { u.Grant.OwnerID = uuid.NewString() },
		} {
			t.Run(name, func(t *testing.T) {
				u := f.usage
				mutate(&u)
				if _, err := QueueRegionalRuntimeUsage(t.Context(), f.store, f.cells, f.envelope(t, u), f.now.Add(time.Second)); !errors.Is(err, ErrConflict) {
					t.Fatal("conflicting usage replayed", err)
				}
			})
		}
		_, re := f.quota.retirement(t, f.quota.grant)
		if _, err := f.quota.authority.AcceptRetirement(ctx, re); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.ScheduleAppDeletion(ctx, f.usage.Grant.Binding.RegionalAppID, time.Now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := f.store.ClaimAppDeletion(ctx, f.usage.Grant.Binding.RegionalAppID); err != nil {
			t.Fatal(err)
		}
		if err := f.store.DeleteAppPermanently(ctx, f.usage.Grant.Binding.RegionalAppID); err != nil {
			t.Fatal(err)
		}
		if r, err := QueueRegionalRuntimeUsage(ctx, f.store, f.cells, f.envelope(t, f.usage), f.now); err != nil || !reflect.DeepEqual(r, first) {
			t.Fatal("collection replaced original identity", r, err)
		}
		ack := runtimequota.UsageImportAck{Usage: f.usage, ImportedAtMicros: f.now.UnixMicro()}
		signed, err := f.quota.signer.SignUsageImportAck(ack)
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := AcknowledgeRegionalRuntimeUsage(ctx, f.store, f.dirs, signed, f.now)
		if err != nil {
			t.Fatal(err)
		}
		_, key, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		directory, err := runtimequota.NewSigner(f.quota.signer.IssuerID(), key)
		if err != nil {
			t.Fatal(err)
		}
		da, err := directory.PublicAnchor(runtimequota.Directory)
		if err != nil {
			t.Fatal(err)
		}
		dirs, err := runtimequota.NewTrust([]runtimequota.Anchor{da})
		if err != nil {
			t.Fatal(err)
		}
		rotatedAck, err := directory.SignUsageImportAck(ack)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := AcknowledgeRegionalRuntimeUsage(ctx, f.store, dirs, rotatedAck, f.now); err != nil || !reflect.DeepEqual(got, accepted) {
			t.Fatal("directory rotation replaced first acknowledgement", got, err)
		}
		ack.ImportedAtMicros++
		conflict, err := directory.SignUsageImportAck(ack)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AcknowledgeRegionalRuntimeUsage(ctx, f.store, dirs, conflict, f.now.Add(time.Second)); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	})
	t.Run("zero-coverage-paging-and-trust", func(t *testing.T) {
		f := open(t)
		u := f.usage
		u.MBSeconds, u.SourceCount = 0, 0
		first, err := QueueRegionalRuntimeUsage(t.Context(), f.store, f.cells, f.envelope(t, u), f.now)
		if err != nil {
			t.Fatal(err)
		}
		u.MinuteMicros -= time.Minute.Microseconds()
		if _, err := f.cell.SignRuntimeUsage(u); err == nil {
			t.Fatal("signed before original issuance")
		}
		for _, limit := range []int{0, api.RegionalRuntimeUsagePageMax + 1} {
			if _, err := f.store.ListPendingRegionalRuntimeUsage(t.Context(), "", 0, limit); !errors.Is(err, ErrInvalidArgument) {
				t.Fatal(err)
			}
		}
		if _, err := f.store.ListPendingRegionalRuntimeUsage(t.Context(), "bad", 0, 1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
		if _, err := f.store.ListPendingRegionalRuntimeUsage(t.Context(), "", -1, 1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
		if page, err := f.store.ListPendingRegionalRuntimeUsage(t.Context(), uuid.NewString(), 0, 1); err != nil || len(page) != 0 {
			t.Fatal(page, err)
		}
		if page, err := f.store.ListPendingRegionalRuntimeUsage(t.Context(), f.cell.IssuerID(), first.ID, 1); err != nil || len(page) != 0 {
			t.Fatal(page, err)
		}
		e := f.envelope(t, f.usage)
		if _, err := QueueRegionalRuntimeUsage(t.Context(), f.store, f.dirs, e, f.now); !errors.Is(err, runtimequota.ErrTrust) {
			t.Fatal(err)
		}
		if _, err := QueueRegionalRuntimeUsage(t.Context(), f.store, f.cells, e, time.UnixMicro(f.usage.ObservedAtMicros-1)); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
		undelivered := f.usage
		undelivered.Grant.AllocationID = uuid.NewString()
		if _, err := QueueRegionalRuntimeUsage(t.Context(), f.store, f.cells, f.envelope(t, undelivered), f.now); !errors.Is(err, ErrNotFound) {
			t.Fatal("usage without original grant delivery queued", err)
		}
		ack := runtimequota.UsageImportAck{Usage: f.usage, ImportedAtMicros: f.now.UnixMicro()}
		signed, err := f.quota.signer.SignUsageImportAck(ack)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AcknowledgeRegionalRuntimeUsage(t.Context(), f.store, f.dirs, signed, f.now); !errors.Is(err, ErrConflict) {
			t.Fatal("positive ack confirmed zero minute", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.store.ListPendingRegionalRuntimeUsage(ctx, "", 0, 1); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestMemRegionalRuntimeUsageOutboxContract(t *testing.T) {
	runRegionalUsageOutboxContract(t, func(t *testing.T) regionalUsageFixture {
		s := NewMemStore()
		a, err := s.CreateAccount(t.Context(), uuid.NewString()+"@regional-usage.example", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		return newRegionalUsageFixture(t, s, a, uuid.NewString())
	})
}
