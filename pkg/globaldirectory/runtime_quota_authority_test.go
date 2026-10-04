package globaldirectory

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type quotaLostIssueResponse struct {
	RuntimeQuotaStore
	lost bool
}

func (s *quotaLostIssueResponse) IssueRuntimeQuotaTarget(ctx context.Context, owner, slug, id, region string) (RuntimeQuotaTarget, error) {
	t, err := s.RuntimeQuotaStore.IssueRuntimeQuotaTarget(ctx, owner, slug, id, region)
	if err == nil && !s.lost {
		s.lost = true
		return RuntimeQuotaTarget{}, errors.New("committed issuance response lost")
	}
	return t, err
}

func exerciseQuotaAuthorityProtocol(t *testing.T, f quotaFixture) {
	t.Helper()
	a, err := f.quota.ReserveRuntimeQuota(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := runtimequota.NewSigner(uuid.NewString(), private)
	if err != nil {
		t.Fatal(err)
	}
	region := f.request.Shares[0].Region
	// Configuration failures do not consume an issuance marker.
	q := RuntimeQuotaAuthority{Store: f.quota}
	if _, err = q.Issue(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, region); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatalf("missing signer: %v", err)
	}
	stored, err := f.quota.RuntimeQuota(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID)
	if err != nil || stored.Targets[0].IssuedAt != nil {
		t.Fatalf("missing signer marked issued: %+v %v", stored, err)
	}
	// A lost commit response leaves the charge durable and retryable.
	q.Store = &quotaLostIssueResponse{RuntimeQuotaStore: f.quota}
	q.Signer = signer
	if envelope, err := q.Issue(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, region); err == nil || envelope.Payload != "" {
		t.Fatal("lost response returned authority")
	}
	stored, err = f.quota.RuntimeQuota(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID)
	if err != nil || stored.Targets[0].IssuedAt == nil {
		t.Fatalf("issued marker rolled back: %+v %v", stored, err)
	}
	issued, err := q.Issue(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, region)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := q.Issue(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, region); err != nil || retry != issued {
		t.Fatalf("issuance retry changed: %v", err)
	}
	target, err := quotaTarget(stored, region)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := quotaWireGrant(signer.IssuerID(), stored, target)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: signer.IssuerID(), Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	if verified, err := trust.VerifyGrant(issued, grant.Binding); err != nil || verified != grant {
		t.Fatalf("issued grant identity: %+v %v", verified, err)
	}
	retired, err := f.quota.RetireRuntimeQuota(t.Context(), f.owner.ID, f.actor.ID, f.request.Slug, a.AllocationID)
	if err != nil || retired.Targets[0].ReleasedAt != nil {
		t.Fatalf("issued capacity lost on retire: %+v %v", retired, err)
	}
	if _, err = q.Issue(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, region); !errors.Is(err, ErrRuntimeQuotaRetired) {
		t.Fatalf("retired grant issued: %v", err)
	}
	envelope, err := q.Retirement(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, region)
	if err != nil {
		t.Fatal(err)
	}
	retirement, err := trust.VerifyRetirement(envelope, grant.Binding)
	if err != nil || retirement.Grant != grant {
		t.Fatalf("retirement before grant delivery identity: %+v %v", retirement, err)
	}
	cellPub, cellPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cellSigner, err := runtimequota.NewSigner(target.CellID, cellPrivate)
	if err != nil {
		t.Fatal(err)
	}
	q.Cells, err = runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: target.CellID, Public: cellPub}})
	if err != nil {
		t.Fatal(err)
	}
	proof := runtimequota.Drain{Retirement: retirement, AdmissionFrozen: true}
	for _, mutate := range []func(*runtimequota.Drain){
		func(d *runtimequota.Drain) { d.Retirement.RetirementID = uuid.NewString() },
		func(d *runtimequota.Drain) { d.Retirement.Grant.Generation++ },
		func(d *runtimequota.Drain) { d.Retirement.Grant.OwnerID = uuid.NewString() },
		func(d *runtimequota.Drain) { d.Retirement.Grant.Binding.APIBase = "https://replaced.example.com" },
		func(d *runtimequota.Drain) { d.Retirement.RetiredAtMicros++ },
	} {
		bad := proof
		mutate(&bad)
		receipt, err := cellSigner.SignDrain(bad)
		if err != nil {
			t.Fatal(err)
		}
		if err = q.ConfirmDrain(t.Context(), f.owner.ID, f.request.Slug, receipt); !errors.Is(err, ErrRuntimeQuotaIdentity) {
			t.Fatalf("forged identity accepted: %v", err)
		}
	}
	proofEnvelope, err := cellSigner.SignDrain(proof)
	if err != nil {
		t.Fatal(err)
	}
	// Registry disable does not invalidate an exact delayed original-cell proof.
	cell, err := f.store.CellByRegion(t.Context(), region)
	if err != nil {
		t.Fatal(err)
	}
	cell.Enabled = false
	if _, err = f.store.PutCell(t.Context(), cell); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = q.ConfirmDrain(t.Context(), f.owner.ID, f.request.Slug, proofEnvelope); err != nil {
			t.Fatalf("exact delayed/duplicate proof: %v", err)
		}
	}
	stored, err = f.quota.RuntimeQuota(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID)
	if err != nil || stored.Targets[0].ReleasedAt == nil {
		t.Fatalf("verified drain did not release: %+v %v", stored, err)
	}
}

func TestGlobalRuntimeQuotaAuthorityMemory(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "personal"
		if shared {
			name = "organization"
		}
		t.Run(name, func(t *testing.T) { exerciseQuotaAuthorityProtocol(t, memoryQuotaFixture(t, shared, 1)) })
	}
}
