package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// publishTXT points the TXT resolver at records; any other name resolves
// to nothing.
func publishTXT(t *testing.T, records map[string]string) {
	t.Helper()
	prev := *apidserver.TxtLookupFunc
	*apidserver.TxtLookupFunc = func(_ context.Context, name string) ([]string, error) {
		if value, ok := records[name]; ok {
			return []string{value}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { *apidserver.TxtLookupFunc = prev })
}

// A pending claim by another account blocks a plain create, but the domain's
// owner takes it over by publishing the takeover token the 409 names; the
// poller then verifies the new claim as usual (ADR-20261020090500).
func TestCreateDomain_DNSProofTakesOverAnotherAccountsPendingClaim(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanScale)
	store := *e.Store()
	squatter, err := store.CreateAccount(ctx, "squatter@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	squatterApp, err := store.CreateApp(ctx, state.App{AccountID: squatter.ID, Slug: "squatter", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	const domain = "shop.owner.example.com"
	if _, err := store.CreateCustomDomainIfUnderQuota(ctx, domain, squatterApp.ID, "squatter-token", 10, 10); err != nil {
		t.Fatal(err)
	}
	appID := apidserver.MustSeedApp(t, e, "domain-owner")
	token := state.ClaimTakeoverToken(state.ClaimKindCustomDomain, (*e.Acct()).ID, domain)
	publishTXT(t, nil)

	rec := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: domain, AppID: appID}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeValidation)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(problem.Detail, "_faas-verify."+domain) || !strings.Contains(problem.Detail, token) || !strings.Contains(problem.Hint, token) {
		t.Fatalf("409 does not name the takeover record: %+v", problem)
	}

	publishTXT(t, map[string]string{"_faas-verify." + domain: token})
	rec = e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: domain, AppID: appID}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry with the record published: status=%d body=%s", rec.Code, rec.Body.String())
	}
	claim, err := store.DomainByName(ctx, domain)
	if err != nil || claim.AppID != appID || claim.ChallengeToken != token {
		t.Fatalf("claim after takeover = %+v, %v; want the owner's app and token", claim, err)
	}
	(*e.S()).RunVerifyOnce(ctx, slog.New(slog.DiscardHandler))
	if claim, _ = store.DomainByName(ctx, domain); !claim.Verified() {
		t.Fatal("the poller did not verify the taken-over claim")
	}
}

// The same takeover works for a tenant hostname, and a surface's unverified
// hostnames are capped.
func TestTenantHostnames_DNSProofTakeoverAndPendingCap(t *testing.T) {
	apidserver.WithTenantSurfacesEnabled(t)
	ctx := context.Background()
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanHobby)
	store := *e.Store()
	lim, _ := api.LimitsFor(api.PlanHobby)
	squatter, err := store.CreateAccount(ctx, "hostname-squatter@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	squatterApp, err := store.CreateApp(ctx, state.App{AccountID: squatter.ID, Slug: "hostname-squatter", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	squatterSurface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: squatter.ID, AppID: squatterApp.ID, Name: "squat"}, lim)
	if err != nil {
		t.Fatal(err)
	}
	const host = "portal.customer.example.com"
	if _, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: squatterSurface.ID, Hostname: host, ChallengeToken: "squat"}, lim); err != nil {
		t.Fatal(err)
	}

	appID := apidserver.MustSeedApp(t, e, "hostname-owner")
	created := e.Do(t, http.MethodPost, "/v1/apps/hostname-owner/tenant-surfaces", api.CreateTenantSurfaceRequest{
		AppID: appID, Name: "owner", CertKind: string(state.CertKindPerHostSAN),
	}, nil)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create surface: status=%d body=%s", created.Code, created.Body.String())
	}
	var surf api.TenantSurfaceResponse
	if err := json.Unmarshal(created.Body.Bytes(), &surf); err != nil {
		t.Fatal(err)
	}
	add := func(hostname string) int {
		return e.Do(t, http.MethodPost, "/v1/apps/hostname-owner/tenant-surfaces/"+surf.ID+"/hostnames", api.AddTenantHostnameRequest{Hostname: hostname}, nil).Code
	}
	token := state.ClaimTakeoverToken(state.ClaimKindTenantHostname, (*e.Acct()).ID, host)
	publishTXT(t, nil)
	if code := add(host); code != http.StatusConflict {
		t.Fatalf("add over a pending claim without proof: status %d, want 409", code)
	}
	publishTXT(t, map[string]string{"_faas-verify." + host: token})
	if code := add(host); code != http.StatusAccepted {
		t.Fatalf("add with the takeover record published: status %d, want 202", code)
	}
	if got, err := store.GetTenantHostnameByName(ctx, host); err != nil || got.SurfaceID != surf.ID || got.ChallengeToken != token {
		t.Fatalf("hostname after takeover = %+v, %v", got, err)
	}

	for i := 1; i < lim.PendingTenantHostnamesPerSurface(); i++ {
		if code := add(fmt.Sprintf("pending-%02d.customer.example.com", i)); code != http.StatusAccepted {
			t.Fatalf("pending hostname %d: status %d", i, code)
		}
	}
	rec := e.Do(t, http.MethodPost, "/v1/apps/hostname-owner/tenant-surfaces/"+surf.ID+"/hostnames", api.AddTenantHostnameRequest{Hostname: "one-too-many.customer.example.com"}, nil)
	apidserver.AssertProblem(t, rec, http.StatusForbidden, api.CodeTenantHostnameQuota)
}

// A surface create that fails on one hostname rolls back the hostnames it
// already added; on the soft-deleted surface they would keep their names
// reserved with no API left to remove them.
func TestCreateTenantSurface_FailedHostnameReleasesEarlierOnes(t *testing.T) {
	apidserver.WithTenantSurfacesEnabled(t)
	ctx := context.Background()
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	store := *e.Store()
	lim, _ := api.LimitsFor(api.PlanPro)
	other, err := store.CreateAccount(ctx, "verified-holder@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "verified-holder", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	otherSurface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: other.ID, AppID: otherApp.ID, Name: "held"}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: otherSurface.ID, Hostname: "taken.example.com", ChallengeToken: "tok"}, lim); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.MarkTenantHostnameVerifiedIfChallenge(ctx, "taken.example.com", "tok"); err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	appID := apidserver.MustSeedApp(t, e, "rollback-owner")
	rec := e.Do(t, http.MethodPost, "/v1/apps/rollback-owner/tenant-surfaces", api.CreateTenantSurfaceRequest{
		AppID: appID, Name: "rollback", CertKind: string(state.CertKindPerHostSAN),
		Hostnames: []string{"first.example.com", "taken.example.com"},
	}, nil)
	apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeTenantHostnameAlreadyClaimed)
	rec = e.Do(t, http.MethodPost, "/v1/apps/rollback-owner/tenant-surfaces", api.CreateTenantSurfaceRequest{
		AppID: appID, Name: "retry", CertKind: string(state.CertKindPerHostSAN), Hostnames: []string{"first.example.com"},
	}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first.example.com stayed reserved by the rolled-back surface: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
