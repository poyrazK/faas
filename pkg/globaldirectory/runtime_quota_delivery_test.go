package globaldirectory

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func TestRuntimeQuotaDeliveryRefusesRedirectAndRetainsIssuedCharge(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	var forwarded atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(http.StatusOK) }))
	t.Cleanup(destination.Close)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(origin.Close)
	cell, err := f.store.CellByRegion(t.Context(), "eu-fsn")
	if err != nil {
		t.Fatal(err)
	}
	cell.APIBase = origin.URL
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
	if _, err := f.store.AttachTarget(t.Context(), f.owner.ID, f.request.Slug, b); err != nil {
		t.Fatal(err)
	}
	if _, err := f.quota.ReserveRuntimeQuota(t.Context(), f.request); err != nil {
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
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: signer.IssuerID(), Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	q := RuntimeQuotaAuthority{Store: f.quota, Signer: signer, Directories: trust}
	key, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := q.Deliver(t.Context(), Verifier{Client: origin.Client()}, f.owner.ID, f.request.Slug, f.request.AllocationID, "eu-fsn", key); !errors.Is(err, ErrVerification) {
			t.Fatal(err)
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("regional admin credential followed redirect")
	}
	a, err := f.quota.RuntimeQuota(t.Context(), f.owner.ID, f.request.Slug, f.request.AllocationID)
	if err != nil || a.Targets[0].IssuedAt == nil || a.Targets[0].ReleasedAt != nil {
		t.Fatal("uncertain delivery lost issued charge", err)
	}
}
