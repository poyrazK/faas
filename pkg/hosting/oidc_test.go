package hosting

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHostingOIDCTrustHasNoWildcardOrPullRequestGrants(t *testing.T) {
	valid := api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push"}
	if !ValidHostingOIDCTrust(valid) {
		t.Fatal("valid binding rejected")
	}
	for _, change := range []func(*api.HostingOIDCTrustRules){
		func(r *api.HostingOIDCTrustRules) { r.RepositoryID = "*" }, func(r *api.HostingOIDCTrustRules) { r.RepositoryOwnerID = "org" }, func(r *api.HostingOIDCTrustRules) { r.Ref = "refs/pull/1/merge" }, func(r *api.HostingOIDCTrustRules) { r.Ref = "refs/heads/*" }, func(r *api.HostingOIDCTrustRules) { r.Ref = "refs/heads/a\x01" }, func(r *api.HostingOIDCTrustRules) {
			r.WorkflowRef = "other/site/.github/workflows/preview.yml@refs/heads/main"
		}, func(r *api.HostingOIDCTrustRules) {
			r.WorkflowRef = "org/site/.github/workflows/a.yml@refs/heads/other"
		}, func(r *api.HostingOIDCTrustRules) {
			r.WorkflowRef = "org/site/.github/workflows/" + strings.Repeat("a", 600) + ".yml@refs/heads/main"
		}, func(r *api.HostingOIDCTrustRules) { r.EventName = "pull_request" }, func(r *api.HostingOIDCTrustRules) { r.EventName = "pull_request_target" },
	} {
		r := valid
		change(&r)
		if ValidHostingOIDCTrust(r) {
			t.Fatal("unsafe binding accepted", r)
		}
	}
}
