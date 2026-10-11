package meter_test

// adr: 798
import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGlobalRuntimeUsagePusherWaitsForAllSelectedRegions(t *testing.T) {
	for _, test := range []struct {
		name                   string
		pending, historicalGap bool
	}{
		{name: "hour"},
		{name: "pending", pending: true},
		{name: "hour_previous_hour_gap", historicalGap: true},
		{name: "pending_previous_hour_gap", pending: true, historicalGap: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
			if err := store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
				t.Fatal(err)
			}
			directory := globaldirectory.NewMemoryStoreWithOrgAuthority(store)
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			dirSigner, err := runtimequota.NewSigner(uuid.NewString(), key)
			if err != nil {
				t.Fatal(err)
			}
			anchor, err := dirSigner.PublicAnchor(runtimequota.Directory)
			if err != nil {
				t.Fatal(err)
			}
			dirTrust, err := runtimequota.NewTrust([]runtimequota.Anchor{anchor})
			if err != nil {
				t.Fatal(err)
			}
			if err := directory.BindRuntimeQuotaIssuer(ctx, dirSigner.IssuerID()); err != nil {
				t.Fatal(err)
			}
			app, err := directory.CreateApp(ctx, acct.ID, "global-billing", 5)
			if err != nil {
				t.Fatal(err)
			}
			bindings := []runtimequota.Binding{}
			signers := []runtimequota.Signer{}
			anchors := []runtimequota.Anchor{}
			request := globaldirectory.RuntimeQuotaRequest{OwnerID: acct.ID, ActorID: acct.ID, Slug: app.Slug, AllocationID: uuid.NewString(), ExpectedPlan: acct.Plan}
			for _, region := range []string{"eu-fsn", "eu-hel"} {
				cell, err := directory.PutCell(ctx, globaldirectory.Cell{ID: uuid.NewString(), Region: region, APIBase: "https://" + region + ".example.com", Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				binding := globaldirectory.Binding{CellID: cell.ID, Region: region, CellGeneration: cell.Generation, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "billing-regional"}
				if _, err := directory.AttachTarget(ctx, acct.ID, app.Slug, binding); err != nil {
					t.Fatal(err)
				}
				bindings = append(bindings, runtimequota.Binding{CellID: cell.ID, Region: region, CellGeneration: cell.Generation, RegionalAccountID: binding.RegionalAccountID, RegionalAppID: binding.RegionalAppID, RegionalAppSlug: binding.RegionalAppSlug, APIBase: cell.APIBase})
				pub, key, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				signer, err := runtimequota.NewSigner(cell.ID, key)
				if err != nil {
					t.Fatal(err)
				}
				signers = append(signers, signer)
				anchors = append(anchors, runtimequota.Anchor{Role: runtimequota.Cell, IssuerID: cell.ID, Public: pub})
				request.Shares = append(request.Shares, globaldirectory.RuntimeQuotaShare{Region: region, Concurrency: 1})
			}
			if _, err := directory.ReserveRuntimeQuota(ctx, request); err != nil {
				t.Fatal(err)
			}
			grants := []runtimequota.Grant{}
			authority := globaldirectory.RuntimeQuotaAuthority{Store: directory, Signer: dirSigner}
			for _, b := range bindings {
				e, err := authority.Issue(ctx, acct.ID, app.Slug, request.AllocationID, b.Region)
				if err != nil {
					t.Fatal(err)
				}
				g, err := dirTrust.VerifyGrant(e, b)
				if err != nil {
					t.Fatal(err)
				}
				grants = append(grants, g)
			}
			firstMinute := time.UnixMicro(grants[0].IssuedAtMicros).UTC().Truncate(time.Minute)
			billMinute := time.UnixMicro(grants[len(grants)-1].IssuedAtMicros).UTC().Truncate(time.Minute)
			gapMinute := firstMinute
			if test.historicalGap {
				billMinute = billMinute.Add(time.Hour)
				// Keep the earlier gap in the billed month at a UTC month boundary.
				if !meter.AccountMonthKey(firstMinute).Equal(meter.AccountMonthKey(billMinute)) {
					billMinute = billMinute.Add(time.Hour)
					gapMinute = meter.AccountMonthKey(billMinute)
				}
			}
			hour := billMinute.Truncate(time.Hour)
			end := hour.Add(time.Hour)
			now := end.Add(time.Minute)
			trust, err := runtimequota.NewTrust(anchors)
			if err != nil {
				t.Fatal(err)
			}
			importer := meter.GlobalRuntimeUsageImporter{Store: store, Cells: trust, Now: func() time.Time { return now }}
			digest := sha256.Sum256([]byte("durable original cell billing intervals"))
			last := runtimequota.Envelope{}
			for minute := firstMinute; minute.Before(end); minute = minute.Add(time.Minute) {
				for i, g := range grants {
					if minute.Before(time.UnixMicro(g.IssuedAtMicros).UTC().Truncate(time.Minute)) {
						continue
					}
					quantity, count := int64(0), int64(0)
					if minute.Equal(billMinute) {
						quantity = 520 * 17
						count = 1
					}
					u := runtimequota.RuntimeUsage{Grant: g, MinuteMicros: minute.UnixMicro(), MBSeconds: quantity, SourceCount: count, SourceDigest: hex.EncodeToString(digest[:]), ObservedAtMicros: minute.Add(time.Minute).UnixMicro()}
					e, err := signers[i].SignRuntimeUsage(u)
					if err != nil {
						t.Fatal(err)
					}
					missing := i == 1 && minute.Equal(end.Add(-time.Minute))
					if test.historicalGap {
						missing = i == 0 && minute.Equal(gapMinute)
					}
					if missing {
						last = e
						continue
					}
					if _, err := importer.Import(ctx, e); err != nil {
						t.Fatal(err)
					}
				}
			}
			provider := &recordingStripe{}
			pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
			push := func() (int, error) {
				if test.pending {
					return pusher.PushPending(ctx, 30*24*time.Hour)
				}
				return pusher.PushHour(ctx)
			}
			if pushed, err := push(); pushed != 0 || !errors.Is(err, meter.ErrGlobalRuntimeUsageIncomplete) || len(provider.Calls()) != 0 {
				t.Fatalf("partial hour settled: %d %v %+v", pushed, err, provider.Calls())
			}
			if _, err := importer.Import(ctx, last); err != nil {
				t.Fatal(err)
			}
			if pushed, err := push(); pushed != 1 || err != nil {
				t.Fatalf("complete hour: %d %v", pushed, err)
			}
			calls := provider.Calls()
			if len(calls) != 1 || calls[0].AccountID != acct.ID || calls[0].MBSeconds != 2*520*17 {
				t.Fatalf("global bill counted incorrectly: %+v", calls)
			}
			if _, err := importer.Import(ctx, last); err != nil {
				t.Fatal(err)
			}
			if test.pending {
				if pushed, err := push(); pushed != 0 || err != nil || len(provider.Calls()) != 1 {
					t.Fatalf("duplicate import pushed twice: %d %v", pushed, err)
				}
			}
		})
	}
}
