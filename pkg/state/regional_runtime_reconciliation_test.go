package state

// adr: 770
import (
	"crypto/ed25519"
	"crypto/rand"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func runRegionalRuntimeReconciliationContract(t *testing.T, fixture func(*testing.T) regionalCapacityFixture) {
	t.Helper()
	t.Run("independent-first-fences-and-replay", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityWarm)
		f.reserve(t, p)
		f.issue(t, p)
		seat := f.seat(t, p, false)
		f.issueSeat(t, seat)
		cellFence, hostFence := uuid.NewString(), uuid.NewString()
		if _, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p, cellFence); err != nil {
			t.Fatal(err)
		}
		host := runtimequota.CapacityStop{Permit: p, FenceID: hostFence, AdmissionFrozen: true}
		if _, err := f.authority.CompleteHostRetirement(t.Context(), host, f.cell); err == nil {
			t.Fatal("independent fence accepted without explicit reconciliation")
		}
		f.usage(t, 1, 1, 1)
		const n = 8
		type result struct {
			r   RegionalRuntimeCapacityReceipt
			err error
		}
		results := make(chan result, n)
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				r, err := f.authority.CompleteHostRetirementReconciliation(t.Context(), cellFence, host, f.cell)
				results <- result{r, err}
			})
		}
		wg.Wait()
		close(results)
		var closed RegionalRuntimeCapacityReceipt
		for got := range results {
			if got.err != nil || got.r.Stop == nil || got.r.StopEnvelope == nil || got.r.ReleasedAt == nil {
				t.Fatal("reconciliation failed", got.err)
			}
			if closed.Stop != nil && !reflect.DeepEqual(closed, got.r) {
				t.Fatal("parallel reconciliation replaced first evidence")
			}
			closed = got.r
		}
		want := host
		want.FenceID, want.HostFenceID = cellFence, hostFence
		if closed.FenceID != cellFence || *closed.Stop != want || host.FenceID != hostFence || host.HostFenceID != "" {
			t.Fatal("original fences or host assertion changed")
		}
		if got, err := f.authority.Cells.VerifyCapacityStop(*closed.StopEnvelope); err != nil || got != want {
			t.Fatal("signed relation changed", err)
		}
		f.usage(t, 0, 0, 0)
		retained, err := f.ledger.RegionalRuntimeServingSeat(t.Context(), f.app(), seat.ID)
		if err != nil || !retained.ClosedByPhysicalStop || retained.FenceID != cellFence {
			t.Fatal("physical stop did not close original seat", err)
		}
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		rotated, err := runtimequota.NewSigner(f.cell.IssuerID(), private)
		if err != nil {
			t.Fatal(err)
		}
		f.authority.Cells, err = runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: rotated.IssuerID(), Public: pub}})
		if err != nil {
			t.Fatal(err)
		}
		replay, err := f.authority.CompleteHostRetirementReconciliation(t.Context(), cellFence, host, rotated)
		if err != nil || !reflect.DeepEqual(closed, replay) {
			t.Fatal("key rotation replaced first relation", err)
		}
		host.FenceID = uuid.NewString()
		if _, err = f.authority.CompleteHostRetirementReconciliation(t.Context(), cellFence, host, rotated); err == nil {
			t.Fatal("accepted host fence replaced")
		}
	})
	t.Run("invalid-relation-retains-original-hold", func(t *testing.T) {
		f := fixture(t)
		p := f.permit(runtimequota.CapacityAppTask)
		f.reserve(t, p)
		f.issue(t, p)
		cellFence := uuid.NewString()
		if _, err := f.ledger.BeginRegionalRuntimeRetirement(t.Context(), p, cellFence); err != nil {
			t.Fatal(err)
		}
		host := runtimequota.CapacityStop{Permit: p, FenceID: uuid.NewString(), AdmissionFrozen: true}
		for _, mutate := range []func(*runtimequota.CapacityStop){
			func(s *runtimequota.CapacityStop) { s.Permit.RunID = uuid.NewString() },
			func(s *runtimequota.CapacityStop) { s.Permit.HostEpochID = uuid.NewString() },
			func(s *runtimequota.CapacityStop) { s.HostFenceID = uuid.NewString() },
			func(s *runtimequota.CapacityStop) { s.AdmissionFrozen = false },
			func(s *runtimequota.CapacityStop) { s.CountedRuntimes = 1 },
		} {
			bad := host
			mutate(&bad)
			if _, err := f.authority.CompleteHostRetirementReconciliation(t.Context(), cellFence, bad, f.cell); err == nil {
				t.Fatal("invalid host evidence reconciled")
			}
		}
		for _, fence := range []string{"", uuid.NewString(), host.FenceID} {
			if _, err := f.authority.CompleteHostRetirementReconciliation(t.Context(), fence, host, f.cell); err == nil {
				t.Fatal("original cell fence replaced")
			}
		}
		untrusted := f.authority
		untrusted.Cells = runtimequota.Trust{}
		if _, err := untrusted.CompleteHostRetirementReconciliation(t.Context(), cellFence, host, f.cell); err == nil {
			t.Fatal("untrusted cell signed relation")
		}
		retained, err := f.ledger.RegionalRuntimeCapacity(t.Context(), f.app(), p.ID)
		if err != nil || retained.FenceID != cellFence || retained.ReleasedAt != nil || retained.Stop != nil || retained.StopEnvelope != nil {
			t.Fatal("refused relation changed hold", err)
		}
		f.usage(t, 1, 1, 0)
	})
}
