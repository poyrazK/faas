//go:build !no_pg

// adr: 590
// Internal mail authority stays outside customer environment clone settings.
package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMailCloneSchemaRemainsKnownAndPrivateWithServiceDisabled(t *testing.T) {
	t.Setenv("FAAS_INTERNAL_MAIL_ENABLED", "0")
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := state.NewPgStore(*e.Pool())
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: e.Acct().ID, Slug: "private-mail-clone"})
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := store.ProjectEnvironmentCloneSchemaCoverage(t.Context(), e.Acct().ID, project.ID)
	if err != nil || !coverage.Known {
		t.Fatalf("private mail schema must not block existing clone workflows: %+v %v", coverage.Blockers, err)
	}
	response := e.Do(t, http.MethodPost, "/v1/projects/"+project.Slug+"/environment-clones", map[string]any{
		"slug": "stage", "from_environment": "production", "full": true,
	}, nil)
	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != http.StatusConflict || problem.Code != api.CodeFullEnvironmentCloneUnavailable {
		t.Fatalf("existing full-clone boundary changed: %d %s %v", response.Code, response.Body.String(), err)
	}
	for _, private := range []string{
		"internal_mail_", "platform_mail_", "billing_mail_revision", "deployment_mail_revision",
		"mfa_revision", "password_reset_revision", "quota_mail_revision", "subscription_mail_revision",
		"mail_failed_at", "mail_failure_", "magic_link_tokens", "mfa_disable_tokens", "password_reset_tokens",
		"billing_account_nonpayment_deletions", "billing_account_suspensions", "billing_payment_failure_receipts",
		"billing_payment_recovery_receipts", "billing_quota_warnings", "billing_subscription_end_receipts", "billing_subscription_ends",
	} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("private mail state exposed as customer clone configuration: %s", private)
		}
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), e.Acct().ID, project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("full-clone blocker created a partial environment: %v", err)
	}
}
