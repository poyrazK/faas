package apidserver_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDomainManualCheckPostgresReadyResponse(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "manual-pg", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.CreateCustomDomain(t.Context(), "manual-pg.example.com", app.ID, "pg-proof")
	if err != nil {
		t.Fatal(err)
	}
	configureDomainDNS(t)
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "edge.gregale.dev.", nil }
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) { return []string{"8.8.8.8"}, nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	before := *apidserver.TxtLookupFunc
	t.Cleanup(func() { *apidserver.TxtLookupFunc = before })
	*apidserver.TxtLookupFunc = func(context.Context, string) ([]string, error) { return []string{d.ChallengeToken}, nil }
	response := e.Do(t, http.MethodPost, "/v1/domains/"+d.Domain+"/verify", nil, nil)
	var got api.CustomDomainResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil {
		t.Fatalf("verify: %d %s", response.Code, response.Body)
	}
	if !got.HTTPSReadyAt(time.Now()) || len(got.Checks) == 0 {
		t.Fatalf("Postgres observations did not establish readiness: %+v", got)
	}
}

func TestDomainManualCheckVerifiesTXTAndReportsReadiness(t *testing.T) {
	for _, routing := range []string{"edge.gregale.dev.", "wrong.example.net."} {
		t.Run(routing, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			store := *e.Store()
			app := apidserver.MustSeedApp(t, e, "check-now")
			d, err := store.CreateCustomDomain(t.Context(), "check.example.com", app, "proof")
			if err != nil {
				t.Fatal(err)
			}
			configureDomainDNS(t)
			*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return routing, nil }
			*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) {
				if routing == "edge.gregale.dev." {
					return []string{"8.8.8.8"}, nil
				}
				return []string{"1.1.1.1"}, nil
			}
			*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
			before := *apidserver.TxtLookupFunc
			t.Cleanup(func() { *apidserver.TxtLookupFunc = before })
			checks := 0
			*apidserver.TxtLookupFunc = func(context.Context, string) ([]string, error) { checks++; return []string{d.ChallengeToken}, nil }
			for range 2 {
				response := e.Do(t, http.MethodPost, "/v1/domains/"+d.Domain+"/verify", nil, nil)
				var got api.CustomDomainResponse
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &got) != nil {
					t.Fatalf("check: %d %s", response.Code, response.Body)
				}
				if !got.Verified || got.Readiness == nil || len(got.Checks) == 0 {
					t.Fatalf("missing progress: %+v", got)
				}
				want := "ready"
				if routing != "edge.gregale.dev." {
					want = "fix_routing"
				}
				if got.Readiness.Status != want {
					t.Fatalf("got %+v, want %s; checks=%+v", got.Readiness, want, got.Checks)
				}
			}
			if checks != 1 {
				t.Fatalf("cooldown did not coalesce probes: %d", checks)
			}
		})
	}
}

func TestDomainDriftNeedsTwoChecksAndHealthyObservationResetsSequence(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "confirmed-drift")
	const host = "drift-check.example.com"
	domainRecoveryVerified(t, store, host, app, "proof")
	configureDomainDNS(t)
	wrong := true
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) {
		if wrong {
			return "wrong.example.net.", nil
		}
		return "edge.gregale.dev.", nil
	}
	*apidserver.ALookupFunc = func(context.Context, string) ([]string, error) {
		if wrong {
			return []string{"1.1.1.1"}, nil
		}
		return []string{"8.8.8.8"}, nil
	}
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	for i, wantVerified := range []bool{true, true, true, false} {
		wrong = i != 1
		if err := (*e.S()).RunDoctorForDomain(t.Context(), slog.New(slog.DiscardHandler), host); err != nil {
			t.Fatal(err)
		}
		d, err := store.DomainByName(t.Context(), host)
		if err != nil {
			t.Fatal(err)
		}
		if d.Verified() != wantVerified {
			t.Fatalf("pass %d verified=%t", i, d.Verified())
		}
	}
}

func TestDomainManualCheckScheduleAndExpiredClaim(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "check-schedule")
	d, err := store.CreateCustomDomain(t.Context(), "schedule.example.com", app, "proof")
	if err != nil {
		t.Fatal(err)
	}
	first := e.Do(t, http.MethodGet, "/v1/domains/"+d.Domain, nil, nil)
	var got api.CustomDomainResponse
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &got) != nil {
		t.Fatalf("get: %d %s", first.Code, first.Body)
	}
	if got.VerificationNextCheckAt == "" || got.VerificationExpiresAt == "" || got.Readiness == nil || got.Readiness.Status != "awaiting_ownership" {
		t.Fatalf("missing schedule: %+v", got)
	}
	now := time.Now()
	allowed, err := store.BeginCustomDomainManualCheck(t.Context(), d, now, api.DomainVerificationManualCooldown)
	if err != nil || !allowed {
		t.Fatalf("reserve: %t %v", allowed, err)
	}
	allowed, err = store.BeginCustomDomainManualCheck(t.Context(), d, now.Add(time.Second), api.DomainVerificationManualCooldown)
	if err != nil || allowed {
		t.Fatalf("cooldown: %t %v", allowed, err)
	}
	d.ChallengeToken = "replacement"
	allowed, err = store.BeginCustomDomainManualCheck(t.Context(), d, now.Add(time.Minute), api.DomainVerificationManualCooldown)
	if err != nil || allowed {
		t.Fatalf("stale claim: %t %v", allowed, err)
	}
}

func TestDomainManualCheckRejectsProofForReplacementClaim(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "replaced-proof")
	d, err := store.CreateCustomDomain(t.Context(), "replaced.example.com", app, "old-proof")
	if err != nil {
		t.Fatal(err)
	}
	before := *apidserver.TxtLookupFunc
	t.Cleanup(func() { *apidserver.TxtLookupFunc = before })
	*apidserver.TxtLookupFunc = func(ctx context.Context, _ string) ([]string, error) {
		if err := store.DeleteCustomDomain(ctx, d.Domain); err != nil {
			return nil, err
		}
		if _, err := store.CreateCustomDomain(ctx, d.Domain, app, "replacement-proof"); err != nil {
			return nil, err
		}
		return []string{d.ChallengeToken}, nil
	}
	response := e.Do(t, http.MethodPost, "/v1/domains/"+d.Domain+"/verify", nil, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("stale check: %d %s", response.Code, response.Body)
	}
	current, err := store.DomainByName(t.Context(), d.Domain)
	if err != nil || current.Verified() || current.ChallengeToken != "replacement-proof" {
		t.Fatalf("old proof verified replacement: %+v %v", current, err)
	}
}

func TestDomainManualRetrySharesCheckCooldown(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "retry-cooldown")
	d, err := store.CreateCustomDomain(t.Context(), "retry.example.com", app, "proof")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimCustomDomainsForVerification(t.Context(), 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	allowed, err := store.BeginCustomDomainManualCheck(t.Context(), d, time.Now(), api.DomainVerificationManualCooldown)
	if err != nil || !allowed {
		t.Fatalf("manual lease: %t %v", allowed, err)
	}
	response := e.Do(t, http.MethodPost, "/v1/domains/"+d.Domain+"/retry", nil, nil)
	if response.Code != http.StatusAccepted || response.Header().Get("Retry-After") == "" {
		t.Fatalf("retry: %d %s", response.Code, response.Body)
	}
	current, err := store.DomainByName(t.Context(), d.Domain)
	if err != nil || current.VerificationAttempts != claimed[0].VerificationAttempts || !current.VerificationNextCheckAt.Equal(claimed[0].VerificationNextCheckAt) {
		t.Fatalf("retry bypassed the cooldown: %+v %v", current, err)
	}
}
