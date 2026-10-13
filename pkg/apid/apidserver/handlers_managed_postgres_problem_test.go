package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresNotConfiguredProblemExplainsPreviewGate(t *testing.T) {
	recorder := httptest.NewRecorder()
	apidserver.ManagedPostgresNotConfiguredProblem(recorder)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var problem api.Problem
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "managed_postgres_unavailable" {
		t.Fatalf("code = %q", problem.Code)
	}
	for _, want := range []string{"operator preview", "not configured", "qualify and configure a provider"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("detail %q does not contain %q", problem.Detail, want)
		}
	}
	if problem.DocsURL != "https://gregale.dev/docs/managed-postgres" {
		t.Fatalf("docs_url = %q", problem.DocsURL)
	}
}

func TestManagedPostgresProviderUnavailableProblemExplainsRetry(t *testing.T) {
	recorder := httptest.NewRecorder()
	apidserver.ManagedPostgresProblem(recorder, managedpostgres.ErrUnavailable)

	var problem api.Problem
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"operator preview", "temporarily unavailable", "retry later"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("detail %q does not contain %q", problem.Detail, want)
		}
	}
}

// TestManagedPostgresUsageStaleProblemExplainsWait pins H9-P101: the stale
// admission gate answered with the generic "could not be completed", so a
// customer whose recently deleted database blocked creates for hours had no
// way to tell a wait from an outage.
func TestManagedPostgresUsageStaleProblemExplainsWait(t *testing.T) {
	recorder := httptest.NewRecorder()
	apidserver.ManagedPostgresProblem(recorder, managedpostgres.ErrUsageStale)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var problem api.Problem
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "managed_postgres_usage_stale" {
		t.Fatalf("code = %q", problem.Code)
	}
	for _, want := range []string{"has not caught up", "deleted", "Existing databases keep running", "retry later"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("detail %q does not contain %q", problem.Detail, want)
		}
	}
}

// TestManagedPostgresProvisioningClosedIsNotAnOutage pins H9-P107: an
// expired canary window answered "the configured provider is temporarily
// unavailable", so a closed rollout gate read like a provider outage.
func TestManagedPostgresProvisioningClosedIsNotAnOutage(t *testing.T) {
	recorder := httptest.NewRecorder()
	apidserver.ManagedPostgresProblem(recorder, managedpostgres.ErrProvisioningClosed)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var problem api.Problem
	if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "managed_postgres_provisioning_not_enabled" {
		t.Fatalf("code = %q", problem.Code)
	}
	if !strings.Contains(problem.Detail, "not open for this account") || strings.Contains(problem.Detail, "temporarily unavailable") {
		t.Fatalf("detail = %q", problem.Detail)
	}
	if problem.DocsURL != "https://gregale.dev/docs/managed-postgres" {
		t.Fatalf("docs_url = %q", problem.DocsURL)
	}
}

// TestManagedPostgresPlanDenialIsNotAQuota pins H9-P114: turning
// scale-to-zero off on a plan without always-on compute answered
// "quota exceeded" with a hint about credentials.
func TestManagedPostgresPlanDenialIsNotAQuota(t *testing.T) {
	for _, tc := range []struct {
		err        error
		code, want string
	}{
		{managedpostgres.ErrNotInPlan, "managed_postgres_not_in_plan", "not included in this plan"},
		{managedpostgres.ErrQuotaExceeded, "managed_postgres_quota_exceeded", "quota exceeded"},
	} {
		recorder := httptest.NewRecorder()
		apidserver.ManagedPostgresProblem(recorder, tc.err)
		var problem api.Problem
		if err := json.NewDecoder(recorder.Body).Decode(&problem); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != http.StatusForbidden || problem.Code != tc.code || !strings.Contains(problem.Detail, tc.want) {
			t.Fatalf("%v → status %d code %q detail %q", tc.err, recorder.Code, problem.Code, problem.Detail)
		}
	}
}
