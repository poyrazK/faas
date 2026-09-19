package main

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func setSecurityPolicyForTest(t *testing.T, e testEnv, slug string, policy api.AppSecurityPolicy) {
	t.Helper()
	appID := findAppID(t, e, slug)
	if _, err := e.store.UpdateApp(t.Context(), appID, state.UpdateAppParams{
		SecurityPolicy:    &policy,
		SetSecurityPolicy: true,
	}); err != nil {
		t.Fatalf("UpdateApp security policy: %v", err)
	}
}

func TestCreateDeployment_SecurityPostureEnforcement(t *testing.T) {
	t.Run("enforce-blocks-high-finding", func(t *testing.T) {
		e := setup(t, api.PlanFree)
		if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "security-enforce"}, nil); rec.Code != http.StatusCreated {
			t.Fatalf("create app: %d %s", rec.Code, rec.Body)
		}
		setSecurityPolicyForTest(t, e, "security-enforce", api.AppSecurityPolicyEnforce)

		rec := e.do(t, "POST", "/v1/apps/security-enforce/deployments", api.CreateDeploymentRequest{
			Image: imageRef('f'),
		}, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("deploy status=%d body=%s, want 403", rec.Code, rec.Body)
		}
		assertProblem(t, rec, http.StatusForbidden, api.CodeSecurityPostureBlocked)
	})

	t.Run("warn-allows-deploy", func(t *testing.T) {
		e := setup(t, api.PlanFree)
		if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "security-warn"}, nil); rec.Code != http.StatusCreated {
			t.Fatalf("create app: %d %s", rec.Code, rec.Body)
		}
		setSecurityPolicyForTest(t, e, "security-warn", api.AppSecurityPolicyWarn)

		rec := e.do(t, "POST", "/v1/apps/security-warn/deployments", api.CreateDeploymentRequest{
			Image: imageRef('e'),
		}, nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("deploy status=%d body=%s, want 202", rec.Code, rec.Body)
		}
	})
}
