package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Company projects scan a connected repository ref through their
// organization's own GitHub installation, never a member's personal one, and
// a plan reviewed in one context never applies in another
// (ADR-20261113090000).
func TestOrgProjectSourceRefUsesOrganizationInstallation(t *testing.T) {
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "existing", 9999)
	store, creator := *e.Store(), *e.AcctID()
	gh := *e.Gh()
	ctx := t.Context()
	// A Free organization owns its resources from creation; a Pro one only
	// after billing adoption, so the Pro organization below stays legacy.
	adopted, err := store.CreateOrg(ctx, state.Org{Slug: "source-co", Name: "Source Co", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if !adopted.ResourceOwnershipEnabled {
		t.Fatal("the Free organization does not own its resources")
	}
	legacy, err := store.CreateOrg(ctx, state.Org{Slug: "legacy-co", Name: "Legacy Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for _, org := range []state.Org{adopted, legacy} {
		if err := store.AddOrgMember(ctx, org.ID, creator, state.OrgRoleOwner, nil); err != nil {
			t.Fatal(err)
		}
	}
	_, developer := sharedAppMember(t, store, adopted, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewer := sharedAppMember(t, store, adopted, state.OrgRoleViewer, api.ScopesAdminOnly)
	scan := api.ProjectSourceRefScanRequest{Repo: "onebox-faas/hello", Ref: "main", ProjectSlug: "hello"}
	path := func(org state.Org) string { return "/v1/orgs/" + org.Slug + "/projects/scan/source-ref" }

	// The creator's personal installation 9999 can see the repository, but a
	// company scan never uses it.
	if rec := e.PostWithHeaders(t, path(adopted), scan, developer); rec.Code != http.StatusNotFound {
		t.Fatalf("company scan without an organization installation = %d %s; want 404", rec.Code, rec.Body)
	}
	pinned := scan
	pinned.InstallID = 9999
	if rec := e.PostWithHeaders(t, path(adopted), pinned, developer); rec.Code != http.StatusNotFound {
		t.Fatalf("company scan through a personal installation = %d %s; want 404", rec.Code, rec.Body)
	}
	if *gh.StreamCalls() != 0 {
		t.Fatalf("a refused company scan fetched source %d times", *gh.StreamCalls())
	}
	if rec := e.PostWithHeaders(t, path(adopted), scan, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer company scan = %d %s; want 403", rec.Code, rec.Body)
	}

	// The organization's own installation serves the scan.
	if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: adopted.ID, InstallationID: 7777, AuditGithubLogin: "company-admin"}); err != nil {
		t.Fatal(err)
	}
	(*gh.ReposByInstall())[7777] = []apidserver.Repo{{FullName: "onebox-faas/hello", DefaultBranch: "main"}}
	*gh.StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(*e.Tarball())}
	rec := e.PostWithHeaders(t, path(adopted), scan, developer)
	if rec.Code != http.StatusOK {
		t.Fatalf("company scan through the organization installation = %d %s", rec.Code, rec.Body)
	}
	if *gh.StreamInstID() != 7777 || *gh.StreamAcctID() != adopted.ID {
		t.Fatalf("stream ran as %q through %d; want the organization %q through 7777", *gh.StreamAcctID(), *gh.StreamInstID(), adopted.ID)
	}

	// An organization without its own resources owns no installation.
	_, legacyOwner := sharedAppMember(t, store, legacy, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if rec := e.PostWithHeaders(t, path(legacy), scan, legacyOwner); rec.Code != http.StatusConflict {
		t.Fatalf("legacy company scan = %d %s; want 409", rec.Code, rec.Body)
	}

	// A plan reviewed for a personal project does not apply as a company
	// deploy, even where the company stores projects in the member's namespace.
	*gh.StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(*e.Tarball())}
	personal := e.Post(t, "/v1/projects/scan/source-ref", scan)
	if personal.Code != http.StatusOK {
		t.Fatalf("personal scan = %d %s", personal.Code, personal.Body)
	}
	var plan api.PlanResponse
	if err := json.Unmarshal(personal.Body.Bytes(), &plan); err != nil || plan.PlanToken == "" {
		t.Fatalf("personal plan token: %v %s", err, personal.Body)
	}
	creatorKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(ctx, legacy.ID, creator, hash, "creator-legacy", api.ScopesAdminOnly, nil); err != nil {
		t.Fatal(err)
	}
	applied := e.PostWithHeaders(t, "/v1/orgs/"+legacy.Slug+"/projects/apply-plan", api.ApplyCachedProjectPlanRequest{PlanToken: plan.PlanToken},
		map[string]string{"Authorization": "Bearer " + creatorKey, "Idempotency-Key": "cross-context"})
	if applied.Code != http.StatusGone {
		t.Fatalf("personal plan applied as a company deploy = %d %s; want 410", applied.Code, applied.Body)
	}
}
