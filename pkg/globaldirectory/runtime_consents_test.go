package globaldirectory

// adr: 774
import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func consentFixture(t *testing.T, f quotaFixture) (RuntimeConsentStore, runtimequota.Consent) {
	t.Helper()
	issuer := uuid.NewString()
	if err := f.store.(RuntimeQuotaIssuerStore).BindRuntimeQuotaIssuer(t.Context(), issuer); err != nil {
		t.Fatal(err)
	}
	a, err := f.store.AppBySlug(t.Context(), f.owner.ID, f.request.Slug)
	if err != nil {
		t.Fatal(err)
	}
	target, err := Resolve(t.Context(), f.store, f.owner.ID, f.request.Slug, f.request.Shares[0].Region)
	if err != nil {
		t.Fatal(err)
	}
	c := runtimequota.Consent{ID: uuid.NewString(), IssuerID: issuer, GlobalAppID: a.ID, OwnerID: runtimeConsentOwner(f.owner.ID), Binding: runtimeConsentBinding(target), ExpiresAtMicros: time.Now().Add(time.Hour).UnixMicro()}
	return f.store.(RuntimeConsentStore), c
}

func exerciseRuntimeConsent(t *testing.T, f quotaFixture) {
	t.Helper()
	j, c := consentFixture(t, f)
	ctx := t.Context()
	owner, slug := f.owner.ID, f.request.Slug
	first, err := j.PrepareRuntimeConsent(ctx, owner, slug, c)
	if err != nil || first.Status != RuntimeConsentPending {
		t.Fatal("prepare", first, err)
	}
	if _, err := j.CheckRuntimeConsent(ctx, owner, slug, c.ID); !errors.Is(err, ErrRuntimeConsentUnavailable) {
		t.Fatal("pending allowed dispatch", err)
	}
	if retry, err := j.PrepareRuntimeConsent(ctx, owner, slug, c); err != nil || !retry.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("pending retry replaced identity", err)
	}
	for _, mutate := range []func(*runtimequota.Consent){func(c *runtimequota.Consent) { c.ExpiresAtMicros++ }, func(c *runtimequota.Consent) { c.IssuerID = uuid.NewString() }, func(c *runtimequota.Consent) { c.Binding.APIBase = "https://replacement.example.com" }, func(c *runtimequota.Consent) { c.Binding.CellGeneration++ }} {
		bad := c
		mutate(&bad)
		if _, err := j.PrepareRuntimeConsent(ctx, owner, slug, bad); err == nil {
			t.Fatal("original consent substituted")
		}
	}
	if _, err := j.RuntimeConsent(ctx, uuid.NewString(), slug, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign owner read", err)
	}
	meta := state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	active, err := j.ObserveRuntimeConsent(ctx, owner, slug, c.ID, meta)
	if err != nil || active.Status != RuntimeConsentActive {
		t.Fatal("activate", err)
	}
	*active.RegionalCreatedAt = active.RegionalCreatedAt.Add(-time.Hour)
	if got, err := j.CheckRuntimeConsent(ctx, owner, slug, c.ID); err != nil || !got.RegionalCreatedAt.Equal(meta.CreatedAt) {
		t.Fatal("observation alias escaped", err)
	}
	wrong := meta
	wrong.Consent.GlobalAppID = uuid.NewString()
	if _, err := j.ObserveRuntimeConsent(ctx, owner, slug, c.ID, wrong); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign reply", err)
	}
	if _, err := j.BeginRevokeRuntimeConsent(ctx, owner, slug, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := j.ObserveRuntimeConsent(ctx, owner, slug, c.ID, meta); !errors.Is(err, ErrRuntimeConsentUnavailable) {
		t.Fatal("revoking consent reactivated", err)
	}
	if _, err := j.CheckRuntimeConsent(ctx, owner, slug, c.ID); !errors.Is(err, ErrRuntimeConsentUnavailable) {
		t.Fatal("revoking dispatch", err)
	}
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	meta.RevokedAt = &stamp
	if got, err := j.ObserveRuntimeConsent(ctx, owner, slug, c.ID, meta); err != nil || got.Status != RuntimeConsentRevoked {
		t.Fatal("revoke", err)
	}
	if _, err := j.PrepareRuntimeConsent(ctx, owner, slug, c); !errors.Is(err, ErrRuntimeConsentUnavailable) {
		t.Fatal("revoked UUID resurrected", err)
	}
	next := c
	next.ID = uuid.NewString()
	if _, err := j.PrepareRuntimeConsent(ctx, owner, slug, next); err != nil {
		t.Fatal("explicit renewal", err)
	}
	if err := f.store.DetachTarget(ctx, owner, slug, c.Binding.Region); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareRuntimeConsent(ctx, owner, slug, next); !errors.Is(err, ErrStaleCell) {
		t.Fatal("detached setup", err)
	}
	if got, err := j.RuntimeConsent(ctx, owner, slug, c.ID); err != nil || got.Consent != c || got.Status != RuntimeConsentRevoked {
		t.Fatal("retained original", err)
	}
	if _, err := j.BeginRevokeRuntimeConsent(ctx, owner, slug, next.ID); err != nil {
		t.Fatal("retained revoke", err)
	}
}

func exerciseRuntimeConsentBound(t *testing.T, f quotaFixture) {
	t.Helper()
	j, c := consentFixture(t, f)
	var wg sync.WaitGroup
	errs := make(chan error, api.GlobalRuntimeConsentsPerAppMax+3)
	for range api.GlobalRuntimeConsentsPerAppMax + 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n := c
			n.ID = uuid.NewString()
			_, err := j.PrepareRuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, n)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok, bounds := 0, 0
	for err := range errs {
		var bound *RuntimeConsentLimitError
		switch {
		case err == nil:
			ok++
		case errors.As(err, &bound):
			bounds++
		default:
			t.Fatal(err)
		}
	}
	if ok != api.GlobalRuntimeConsentsPerAppMax || bounds != 3 {
		t.Fatalf("concurrent bound: %d/%d", ok, bounds)
	}
}

func TestDirectoryRuntimeConsentMemory(t *testing.T) {
	exerciseRuntimeConsent(t, memoryQuotaFixture(t, false, 1))
}
func TestDirectoryRuntimeConsentMemoryBound(t *testing.T) {
	exerciseRuntimeConsentBound(t, memoryQuotaFixture(t, false, 1))
}

func TestDirectoryRuntimeConsentOwnerAndExpiry(t *testing.T) {
	for _, mode := range []string{"suspended", "expired", "registry"} {
		t.Run(mode, func(t *testing.T) {
			f := memoryQuotaFixture(t, false, 1)
			j, c := consentFixture(t, f)
			if mode == "expired" {
				c.ExpiresAtMicros = time.Now().Add(60 * time.Millisecond).UnixMicro()
			}
			if _, err := j.PrepareRuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, c); err != nil {
				t.Fatal(err)
			}
			meta := state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
			switch mode {
			case "suspended":
				if err := f.authority.UpdateAccountStatus(t.Context(), f.actor.ID, state.AccountSuspended); err != nil {
					t.Fatal(err)
				}
			case "registry":
				cell, err := f.store.CellByRegion(t.Context(), c.Binding.Region)
				if err != nil {
					t.Fatal(err)
				}
				cell.APIBase = "https://replacement.example.com"
				if _, err = f.store.PutCell(t.Context(), cell); err != nil {
					t.Fatal(err)
				}
			case "expired":
				time.Sleep(time.Until(time.UnixMicro(c.ExpiresAtMicros)) + time.Millisecond)
			}
			if _, err := j.ObserveRuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, c.ID, meta); err == nil {
				t.Fatal("stale owner/scope/deadline activated consent")
			}
			got, err := j.RuntimeConsent(t.Context(), f.owner.ID, f.request.Slug, c.ID)
			if err != nil || got.Status != RuntimeConsentPending {
				t.Fatal("failed activation altered record", err)
			}
		})
	}
}

func TestDirectoryRuntimeConsentHTTPRecovery(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	j, c := consentFixture(t, f)
	owner, slug := f.owner.ID, f.request.Slug
	key, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	var observed *state.RegionalRuntimeConsent
	puts, deletes := 0, 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("wrong provisioning credential")
		}
		retained, err := j.RuntimeConsent(r.Context(), owner, slug, c.ID)
		if err != nil {
			t.Error("intent not committed before I/O", err)
		}
		if r.URL.Path != "/v1/cells/"+c.Binding.CellID+"/apps/"+c.Binding.RegionalAppID+"/runtime-consents/"+c.ID {
			t.Error("wrong original path")
		}
		if r.Method == http.MethodPut {
			if retained.Status != RuntimeConsentPending && retained.Status != RuntimeConsentActive {
				t.Error("wrong setup phase")
			}
			var sent runtimequota.Consent
			if json.NewDecoder(r.Body).Decode(&sent) != nil || sent != c {
				t.Error("changed original scope")
			}
			puts++
			if observed == nil {
				observed = &state.RegionalRuntimeConsent{Consent: sent, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
			}
			if puts == 1 {
				http.Error(w, key, http.StatusServiceUnavailable)
				return
			}
		} else if r.Method == http.MethodDelete {
			if retained.Status != RuntimeConsentRevoking {
				t.Error("revocation not committed before I/O")
			}
			deletes++
			if observed.RevokedAt == nil {
				stamp := time.Now().UTC().Truncate(time.Microsecond)
				observed.RevokedAt = &stamp
			}
			if deletes == 1 {
				http.Error(w, key, http.StatusServiceUnavailable)
				return
			}
		} else {
			t.Error("unexpected method")
		}
		_ = json.NewEncoder(w).Encode(observed)
	}))
	defer srv.Close()
	cell, err := f.store.CellByRegion(t.Context(), c.Binding.Region)
	if err != nil {
		t.Fatal(err)
	}
	cell.APIBase = srv.URL
	cell, err = f.store.PutCell(t.Context(), cell)
	if err != nil {
		t.Fatal(err)
	}
	b := Binding{CellID: cell.ID, Region: cell.Region, CellGeneration: cell.Generation, RegionalAccountID: c.Binding.RegionalAccountID, RegionalAppID: c.Binding.RegionalAppID, RegionalAppSlug: c.Binding.RegionalAppSlug}
	if _, err := f.store.AttachTarget(t.Context(), owner, slug, b); err != nil {
		t.Fatal(err)
	}
	c.Binding.APIBase, c.Binding.CellGeneration = srv.URL, cell.Generation
	svc := RuntimeConsentService{Store: f.store, Journal: j, IssuerID: c.IssuerID, Verifier: Verifier{Client: srv.Client()}}
	if _, err := svc.Setup(t.Context(), owner, slug, c.ID, c.Binding.Region, time.UnixMicro(c.ExpiresAtMicros), key); !errors.Is(err, ErrRuntimeConsentUnavailable) || strings.Contains(err.Error(), key) {
		t.Fatal("lost setup reply", err)
	}
	if got, err := j.RuntimeConsent(t.Context(), owner, slug, c.ID); err != nil || got.Status != RuntimeConsentPending || got.Consent != c {
		t.Fatal("lost response replaced intent", err)
	}
	if got, err := svc.Setup(t.Context(), owner, slug, c.ID, c.Binding.Region, time.UnixMicro(c.ExpiresAtMicros), key); err != nil || got.Status != RuntimeConsentActive {
		t.Fatal("setup retry", err)
	}
	if err := f.store.DetachTarget(t.Context(), owner, slug, c.Binding.Region); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Revoke(t.Context(), owner, slug, c.ID, key); !errors.Is(err, ErrRuntimeConsentUnavailable) || strings.Contains(err.Error(), key) {
		t.Fatal("lost revoke reply", err)
	}
	if got, err := svc.Revoke(t.Context(), owner, slug, c.ID, key); err != nil || got.Status != RuntimeConsentRevoked || puts != 2 || deletes != 2 {
		t.Fatal("original revoke retry", err)
	}
}

func TestDirectoryRuntimeConsentResponseStrict(t *testing.T) {
	f := memoryQuotaFixture(t, false, 1)
	_, c := consentFixture(t, f)
	meta := state.RegionalRuntimeConsent{Consent: c, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decodeRuntimeConsentResponse(raw); err != nil || got.Consent != c {
		t.Fatal(err)
	}
	for _, bad := range []string{string(raw) + "{}", strings.Replace(string(raw), `"created_at":`, `"unknown":1,"created_at":`, 1), strings.Replace(string(raw), `"created_at":`, `"created_at":null,"created_at":`, 1), strings.Replace(string(raw), `"id":`, `"id":null,"id":`, 1), strings.TrimSuffix(string(raw), "}") + `,"revoked_at":null}`, strings.Repeat(" ", api.GlobalDirectoryProofMaxBytes+1)} {
		if _, err := decodeRuntimeConsentResponse([]byte(bad)); err == nil {
			t.Fatal("ambiguous response accepted")
		}
	}
}
