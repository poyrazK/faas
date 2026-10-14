// adr: 1155 — candidate assessment and customer status feedback contracts.
package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestGitOpsDashboardForwardsGitLabBindingIdentity(t *testing.T) {
	server, _, _, _, _ := apidserver.NewProjectLifecycleFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(url.Values{"provider": {"gitlab"}, "repository": {"group/subgroup/shop"}, "ref": {"refs/heads/main"}, "manifest_path": {"environments/staging.yaml"}, "approval_policy": {"manual"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetPathValue("action", "bind")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	body, method, err := server.GitOpsDashboardRequestForTest(request)
	input, ok := body.(api.CreateEnvironmentGitSourceRequest)
	if err != nil || !ok || method != http.MethodPost || input.Provider != "gitlab" || input.Repository != "group/subgroup/shop" || input.Mode != "report" || input.Prune || input.ApprovalPolicy != "manual" {
		t.Fatalf("form lost or changed provider identity: %+v %s %v", body, method, err)
	}
}
