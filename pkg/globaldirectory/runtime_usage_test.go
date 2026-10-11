package globaldirectory

// adr: 798
import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeUsageFixture struct {
	quota       quotaFixture
	store       state.GlobalRuntimeUsageStore
	grants      []runtimequota.Grant
	signers     []runtimequota.Signer
	trust       runtimequota.Trust
	minute, now time.Time
}

func newRuntimeUsageFixture(t *testing.T, f quotaFixture) runtimeUsageFixture {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := runtimequota.NewSigner(uuid.NewString(), private)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.(RuntimeQuotaIssuerStore).BindRuntimeQuotaIssuer(t.Context(), directory.IssuerID()); err != nil {
		t.Fatal(err)
	}
	allocation, err := f.quota.ReserveRuntimeQuota(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	u := runtimeUsageFixture{quota: f, store: f.authority, minute: time.Now().UTC().Truncate(time.Minute)}
	anchors := []runtimequota.Anchor{}
	for _, region := range f.request.Shares {
		target, err := f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, f.request.Slug, allocation.AllocationID, region.Region)
		if err != nil {
			t.Fatal(err)
		}
		g, err := quotaWireGrant(directory.IssuerID(), allocation, target)
		if err != nil {
			t.Fatal(err)
		}
		u.minute = time.UnixMicro(g.IssuedAtMicros).UTC().Truncate(time.Minute)
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := runtimequota.NewSigner(g.Binding.CellID, key)
		if err != nil {
			t.Fatal(err)
		}
		u.grants = append(u.grants, g)
		u.signers = append(u.signers, signer)
		anchors = append(anchors, runtimequota.Anchor{Role: runtimequota.Cell, IssuerID: g.Binding.CellID, Public: pub})
	}
	u.now = u.minute.Add(3 * time.Minute)
	u.trust, err = runtimequota.NewTrust(anchors)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f runtimeUsageFixture) usage(index int, minute time.Time, mbSeconds int64) runtimequota.RuntimeUsage {
	digest := sha256.Sum256([]byte("retained cell resource intervals"))
	count := int64(0)
	if mbSeconds > 0 {
		count = 1
	}
	return runtimequota.RuntimeUsage{Grant: f.grants[index], MinuteMicros: minute.UnixMicro(), MBSeconds: mbSeconds, SourceCount: count, SourceDigest: hex.EncodeToString(digest[:]), ObservedAtMicros: minute.Add(time.Minute).UnixMicro()}
}

func (f runtimeUsageFixture) envelope(t *testing.T, index int, u runtimequota.RuntimeUsage) runtimequota.Envelope {
	t.Helper()
	e, err := f.signers[index].SignRuntimeUsage(u)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (f runtimeUsageFixture) importUsage(t *testing.T, index int, minute time.Time, quantity int64) state.GlobalRuntimeUsageReceipt {
	t.Helper()
	u := f.usage(index, minute, quantity)
	r, err := state.ImportGlobalRuntimeUsage(t.Context(), f.store, f.trust, f.envelope(t, index, u), f.now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func exerciseRuntimeUsageContract(t *testing.T, f runtimeUsageFixture) {
	t.Helper()
	ctx := t.Context()
	owner := f.quota.owner.AccountID
	end := f.minute.Add(time.Minute)
	if ready, err := f.store.GlobalRuntimeBillingReady(ctx, owner, f.minute, end); err != nil || ready {
		t.Fatalf("missing receipt: %v %v", ready, err)
	}
	u := f.usage(0, f.minute, 520*17)
	e := f.envelope(t, 0, u)
	var wg sync.WaitGroup
	results := make(chan state.GlobalRuntimeUsageReceipt, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := state.ImportGlobalRuntimeUsage(ctx, f.store, f.trust, e, f.now)
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
	var first state.GlobalRuntimeUsageReceipt
	for r := range results {
		if first.BillingInstanceID == "" {
			first = r
		}
		if r != first {
			t.Fatal("concurrent import changed first receipt")
		}
	}
	if first.AccountID != owner || first.Usage != u || first.AppSlug != f.quota.request.Slug {
		t.Fatalf("original billing owner: %+v", first)
	}
	// Rotation authenticates a new delivery without replacing the first receipt.
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := runtimequota.NewSigner(u.Grant.Binding.CellID, private)
	if err != nil {
		t.Fatal(err)
	}
	rotatedTrust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: u.Grant.Binding.CellID, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	rotatedEnvelope, err := rotated.SignRuntimeUsage(u)
	if err != nil {
		t.Fatal(err)
	}
	if rotatedEnvelope == e {
		t.Fatal("rotated key did not change the signature")
	}
	if replay, err := state.ImportGlobalRuntimeUsage(ctx, f.store, rotatedTrust, rotatedEnvelope, f.now); err != nil || replay != first {
		t.Fatalf("rotation changed first receipt: %+v %v", replay, err)
	}
	if ready, err := f.store.GlobalRuntimeBillingReady(ctx, owner, f.minute, end); err != nil || ready {
		t.Fatalf("missing second region: %v %v", ready, err)
	}
	second := f.importUsage(t, 1, f.minute, 0)
	if second.BillingInstanceID == first.BillingInstanceID {
		t.Fatal("different cells collapsed into one billing row")
	}
	if ready, err := f.store.GlobalRuntimeBillingReady(ctx, owner, f.minute, end); err != nil || !ready {
		t.Fatalf("complete zero observation: %v %v", ready, err)
	}
	if ready, err := f.store.GlobalRuntimeBillingReady(ctx, owner, f.minute, end.Add(time.Minute)); err != nil || ready {
		t.Fatalf("coverage extrapolated into next minute: %v %v", ready, err)
	}
	rows, err := f.quota.authority.UsageByHour(ctx, owner, f.minute, end)
	if err != nil || len(rows) != 1 || rows[0].AppID != u.Grant.GlobalAppID || rows[0].MBSeconds != u.MBSeconds {
		t.Fatalf("canonical owner usage: %+v %v", rows, err)
	}
	for _, g := range f.grants {
		rows, err := f.quota.authority.UsageByHour(ctx, g.Binding.RegionalAccountID, f.minute, end)
		if err != nil || len(rows) != 0 {
			t.Fatalf("regional account charged: %+v %v", rows, err)
		}
	}
	for name, mutate := range map[string]func(*runtimequota.RuntimeUsage){
		"quantity":         func(u *runtimequota.RuntimeUsage) { u.MBSeconds++ },
		"digest":           func(u *runtimequota.RuntimeUsage) { u.SourceDigest = hex.EncodeToString(make([]byte, 32)) },
		"observation":      func(u *runtimequota.RuntimeUsage) { u.ObservedAtMicros++ },
		"owner":            func(u *runtimequota.RuntimeUsage) { u.Grant.OwnerID = uuid.NewString() },
		"origin":           func(u *runtimequota.RuntimeUsage) { u.Grant.Binding.APIBase = "https://replacement.example.com" },
		"generation":       func(u *runtimequota.RuntimeUsage) { u.Grant.Generation++ },
		"regional account": func(u *runtimequota.RuntimeUsage) { u.Grant.Binding.RegionalAccountID = uuid.NewString() },
		"allocation":       func(u *runtimequota.RuntimeUsage) { u.Grant.AllocationID = uuid.NewString() },
	} {
		t.Run(name, func(t *testing.T) {
			bad := u
			mutate(&bad)
			_, err := state.ImportGlobalRuntimeUsage(t.Context(), f.store, f.trust, f.envelope(t, 0, bad), f.now)
			if err == nil {
				t.Fatal("conflicting original receipt accepted")
			}
		})
	}
	if _, err := state.ImportGlobalRuntimeUsage(ctx, f.store, f.trust, e, f.minute); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("future observation: %v", err)
	}
	// Today's plan and origin are not historical usage authority.
	if err := f.quota.authority.UpdateAccountPlan(ctx, owner, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	cell, err := f.quota.store.CellByRegion(ctx, f.grants[0].Binding.Region)
	if err != nil {
		t.Fatal(err)
	}
	cell.APIBase = "https://replacement.example.com"
	cell.Enabled = false
	if _, err := f.quota.store.PutCell(ctx, cell); err != nil {
		t.Fatal(err)
	}
	if err := f.quota.store.DetachTarget(ctx, owner, f.quota.request.Slug, cell.Region); err != nil {
		t.Fatal(err)
	}
	f.importUsage(t, 0, end, 520*13)
	f.importUsage(t, 1, end, 0)
	financial := f.quota.authority.(state.FinancialStore)
	head, err := financial.FinancialEvidenceHead(ctx, owner, f.minute, end.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := financial.ListFinancialUsageEvidence(ctx, owner, f.minute, end.Add(time.Minute), 0, head, 100)
	if err != nil || len(evidence) != 2 {
		t.Fatalf("retained evidence: %+v %v", evidence, err)
	}
	for _, record := range evidence {
		if record.Plan != api.PlanPro || record.Evidence.Attribution.AppID != u.Grant.GlobalAppID || record.Evidence.Attribution.Name != f.quota.request.Slug {
			t.Fatalf("historical identity repriced: %+v", record)
		}
	}
	if got, err := f.store.GlobalRuntimeUsage(ctx, u.Grant.GlobalAppID, u.Grant.AllocationID, u.Grant.Binding.CellID, f.minute); err != nil || got != first {
		t.Fatalf("retained receipt changed: %+v %v", got, err)
	}
}

func TestGlobalRuntimeUsageMemoryContract(t *testing.T) {
	exerciseRuntimeUsageContract(t, newRuntimeUsageFixture(t, memoryQuotaFixture(t, false, 2)))
}
