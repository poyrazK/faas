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

func TestCustomDomainBulkTenantRejectsForeignWildcard(t *testing.T) {
	t.Setenv("FAAS_TENANT_SURFACES_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	own := apidserver.MustSeedApp(t, e, "bulk-claimant")
	victim := domainRecoveryApp(t, store, "bulk-victim@example.com", "bulk-victim")
	const wildcard = "*.bulk-victim.example.com"
	const host = "live.bulk-victim.example.com"
	domainRecoveryVerified(t, store, wildcard, victim.ID, "victim-proof")
	control := e.Do(t, http.MethodPost, "/v1/apps/bulk-claimant/tenant-surfaces", api.CreateTenantSurfaceRequest{AppID: own, Name: "ordinary", Hostnames: []string{host}}, nil)
	if control.Code != http.StatusConflict {
		t.Fatalf("invalid control: %d %s", control.Code, control.Body)
	}
	req := api.ApplyPlatformTenantRequest{ExternalRef: "bulk-overlap", Name: "Bulk overlap", Surfaces: []api.ApplyPlatformTenantSurfaceRequest{{AppID: own, Name: "bulk-hosts", Hostnames: []string{host}}}}
	result := e.Do(t, http.MethodPost, "/v1/account/platform-tenants/apply", req, nil)
	if result.Code == http.StatusOK {
		var applied api.ApplyPlatformTenantResponse
		if err := json.Unmarshal(result.Body.Bytes(), &applied); err != nil {
			t.Fatal(err)
		}
		suspend := e.Do(t, http.MethodPatch, "/v1/account/platform-tenants/"+applied.TenantID, map[string]string{"status": "suspended"}, nil)
		binding, err := store.PlatformTenantHostBinding(t.Context(), host)
		t.Fatalf("ordinary creation=%d but bulk creation=%d; suspension=%d binding=%+v err=%v; body=%s", control.Code, result.Code, suspend.Code, binding, err, result.Body)
	}
	if result.Code != http.StatusConflict {
		t.Fatalf("unexpected result: %d %s", result.Code, result.Body)
	}
}

func TestCustomDomainExpiredClaimReportsRecoveryAndRequiresNewProof(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := *e.Store()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "expired-onboarding", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	const host = "expired.onboarding.example.com"
	d, err := store.CreateCustomDomain(t.Context(), host, app.ID, "expired-proof")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE custom_domains SET verification_expires_at=$2 WHERE domain=$1", host, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	before := *apidserver.TxtLookupFunc
	t.Cleanup(func() { *apidserver.TxtLookupFunc = before })
	checks := 0
	*apidserver.TxtLookupFunc = func(context.Context, string) ([]string, error) { checks++; return []string{d.ChallengeToken}, nil }
	(*e.S()).RunVerifyOnce(t.Context(), slog.New(slog.DiscardHandler))
	status := e.Do(t, http.MethodGet, "/v1/domains/"+host+"/status", nil, nil)
	verify := e.Do(t, http.MethodPost, "/v1/domains/"+host+"/verify", nil, nil)
	retry := e.Do(t, http.MethodPost, "/v1/domains/"+host+"/retry", nil, nil)
	if status.Code != http.StatusOK || verify.Code != http.StatusOK || retry.Code != http.StatusConflict || checks != 0 {
		t.Fatalf("fixture: status=%d verify=%d retry=%d checks=%d", status.Code, verify.Code, retry.Code, checks)
	}
	var conditionStatus api.ResourceStatus
	if err := json.Unmarshal(status.Body.Bytes(), &conditionStatus); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{api.ConditionReady, api.ConditionVerified} {
		found := false
		for _, c := range conditionStatus.Conditions {
			if c.Type == name {
				found = true
				if c.Status != api.ConditionFalse || !c.Terminal || c.Reason != "verification_expired" {
					t.Errorf("expired claim status: %+v", c)
				}
			}
		}
		if !found {
			t.Errorf("missing %s condition", name)
		}
	}
	var expired api.CustomDomainResponse
	if err := json.Unmarshal(verify.Body.Bytes(), &expired); err != nil {
		t.Fatal(err)
	}
	if !expired.VerificationExpired || expired.VerificationExpiresAt == "" || expired.ChallengeToken != "" || expired.TXTRecord != "" {
		t.Errorf("expired claim still advertises obsolete proof: %+v", expired)
	}
	for _, record := range expired.DNSRecords {
		if record.Purpose == api.DNSRecordPurposeVerification {
			t.Errorf("obsolete DNS instruction: %+v", record)
		}
	}
	fresh := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: host, AppID: "expired-onboarding"}, nil)
	var replacement api.CustomDomainResponse
	if fresh.Code != http.StatusAccepted || json.Unmarshal(fresh.Body.Bytes(), &replacement) != nil || replacement.ChallengeToken == d.ChallengeToken {
		t.Fatalf("re-creation control: %d %s", fresh.Code, fresh.Body)
	}

}

func TestCustomDomainExpiredCertificateIsNotReadyOrHealthy(t *testing.T) {
	t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "expired-certificate")
	const host = "expired.certificate.example.com"
	domainRecoveryVerified(t, store, host, app, "proof")
	expiry := time.Now().Add(-time.Hour)
	if err := store.UpdateCustomDomainCertStatus(t.Context(), host, state.CustomDomainCertIssued, expiry, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	permitted := true
	if err := store.UpsertDoctorObservation(t.Context(), state.DomainDoctorObservation{Domain: host, ObservedAt: time.Now(), DNSRecordFound: true, PointsToGregale: true, CAAPermits: &permitted, CertState: "issued", CertNotAfter: expiry, DNSCheckedAt: time.Now(), CertCheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	status := e.Do(t, http.MethodGet, "/v1/domains/"+host+"/status", nil, nil)
	var result api.ResourceStatus
	if status.Code != 200 || json.Unmarshal(status.Body.Bytes(), &result) != nil {
		t.Fatalf("invalid status: %d %s", status.Code, status.Body)
	}
	for _, condition := range result.Conditions {
		if condition.Type == api.ConditionReady && condition.Status == api.ConditionTrue {
			t.Errorf("expired certificate still ready: %s", status.Body)
		}
	}
	report := e.Do(t, http.MethodGet, "/v1/domains/"+host+"/doctor", nil, nil)
	var doctor api.DomainDoctorReport
	if report.Code != 200 || json.Unmarshal(report.Body.Bytes(), &doctor) != nil {
		t.Fatalf("invalid doctor response: %d %s", report.Code, report.Body)
	}
	if doctor.Healthy {
		t.Errorf("expired certificate doctor reports healthy: %s", report.Body)
	}
}

func TestCustomDomainDoctorDiscardsSupersededDriftAndSideEffects(t *testing.T) {
	t.Setenv("FAAS_DOMAIN_DOCTOR_ENABLED", "true")
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	appID := apidserver.MustSeedApp(t, e, "superseded-doctor")
	const host = "superseded.doctor.example.com"
	domainRecoveryVerified(t, store, host, appID, "proof")
	configureDomainDNS(t)
	*apidserver.CnameLookupFunc = func(context.Context, string) (string, error) { return "foreign.example.net.", nil }
	*apidserver.AAAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	*apidserver.CAALookupFunc = func(context.Context, string) ([]string, error) { return nil, nil }
	// A newer healthy pass finishes while this older mismatch pass is gathering DNS.
	newer := time.Now().UTC().Add(time.Minute)
	healthy := state.DomainDoctorObservation{Domain: host, ObservedAt: newer, DNSCheckedAt: newer, CertCheckedAt: newer, DNSRecordFound: true, PointsToGregale: true, CertState: "issued", CertNotAfter: newer.Add(24 * time.Hour)}
	*apidserver.ALookupFunc = func(ctx context.Context, _ string) ([]string, error) {
		if err := store.UpdateCustomDomainCertStatus(ctx, host, state.CustomDomainCertIssued, healthy.CertNotAfter, "", newer); err != nil {
			return nil, err
		}
		if err := store.UpsertDoctorObservation(ctx, healthy); err != nil {
			return nil, err
		}
		return []string{"1.1.1.1"}, nil
	}
	response := e.Do(t, http.MethodGet, "/v1/domains/"+host+"/doctor", nil, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("superseded refresh should be retryable: %d %s", response.Code, response.Body)
	}
	current, err := store.DomainByName(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Verified() || current.CertStatus != state.CustomDomainCertIssued {
		t.Fatalf("superseded drift revoked route: %+v", current)
	}
	obs, err := store.GetDoctorObservation(t.Context(), host)
	if err != nil || !obs.PointsToGregale || obs.CertState != "issued" || !obs.DNSCheckedAt.Equal(newer) {
		t.Fatalf("older failure replaced newer observation: %+v %v", obs, err)
	}
}
