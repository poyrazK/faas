package gitlabsource

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func reviewResponses() map[string]string {
	sha, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	at := time.Now().UTC().Add(-time.Hour)
	stamp := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339Nano) }
	mr := map[string]any{"id": 101, "iid": 7, "project_id": 41, "source_project_id": 41, "target_project_id": 41, "target_branch": "main", "state": "merged", "sha": head, "merge_commit_sha": sha, "merged_at": stamp(30 * time.Minute), "author": map[string]any{"id": 10}, "diff_refs": map[string]string{"head_sha": head}}
	mrJSON, _ := json.Marshal(mr)
	return map[string]string{
		"/projects/team/subgroup/shop":                               `{"id":41,"path_with_namespace":"team/subgroup/shop"}`,
		"/projects/41/approvals":                                     `{"reset_approvals_on_push":true,"selective_code_owner_removals":false,"disable_overriding_approvers_per_merge_request":true,"merge_requests_author_approval":false,"merge_requests_disable_committers_approval":true}`,
		"/projects/41/protected_branches":                            `[{"id":1,"name":"main","allow_force_push":false,"push_access_levels":[{"access_level":0}],"merge_access_levels":[{"access_level":40}]}]`,
		"/projects/41/approval_rules":                                `[{"id":2,"name":"review","rule_type":"regular","approvals_required":1,"contains_hidden_groups":false,"protected_branches":[]}]`,
		"/projects/41/repository/commits/" + sha + "/merge_requests": "[" + string(mrJSON) + "]",
		"/projects/41/merge_requests/7":                              string(mrJSON),
		"/projects/41/merge_requests/7/versions":                     `[{"id":3,"merge_request_id":101,"head_commit_sha":"` + head + `","created_at":"` + stamp(0) + `","state":"collected","patch_id_sha":"patch"}]`,
		"/projects/41/merge_requests/7/approvals":                    `{"approved":true,"approvals_required":1,"approvals_left":0,"approved_by":[{"user":{"id":11},"approved_at":"` + stamp(10*time.Minute) + `"}]}`,
		"/projects/41/merge_requests/7/approval_state":               `{"approval_rules_overwritten":false,"rules":[{"id":4,"name":"review","rule_type":"regular","approvals_required":1,"contains_hidden_groups":false,"approved":true,"overridden":false,"source_rule":{"id":2},"approved_by":[{"id":11}]}]}`,
		"/users/11":                             `{"id":11,"username":"reviewer","state":"active","bot":false}`,
		"/projects/41/members/all/11":           `{"id":11,"state":"active","access_level":40,"expires_at":null}`,
		"/projects/41/repository/branches/main": `{"commit":{"id":"` + sha + `"}}`,
	}
}

func TestReviewedGitLabMergeUsesFreshProviderBoundEvidence(t *testing.T) {
	responses := reviewResponses()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("invalid provider request")
		}
		body, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	defer server.Close()
	client := Client{APIURL: server.URL, HTTP: server.Client()}
	evidence, err := client.ReviewedMergeEvidence(t.Context(), "secret", 41, "team/subgroup/shop", "main", strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.ValidForProvider("gitlab", 0, 41, "team/subgroup/shop", "main", strings.Repeat("a", 40), time.Now(), time.Minute) || evidence.ValidFor(42, 41, "team/subgroup/shop", "main", strings.Repeat("a", 40), time.Now(), time.Minute) || evidence.HeadSHA != strings.Repeat("b", 40) {
		t.Fatalf("invalid provider evidence: %+v", evidence)
	}
}

func TestReviewedGitLabMergeFailsClosedOnIncompleteOrChangedAuthority(t *testing.T) {
	for _, tc := range []struct{ name, path, old, new string }{
		{"push bypass", "/projects/41/protected_branches", `"access_level":0`, `"access_level":40`},
		{"force push", "/projects/41/protected_branches", `"allow_force_push":false`, `"allow_force_push":true`},
		{"missing push permission", "/projects/41/protected_branches", `"push_access_levels":[{"access_level":0}]`, `"push_access_levels":[]`},
		{"unknown force push policy", "/projects/41/protected_branches", `"allow_force_push":false,`, ``},
		{"no reset", "/projects/41/approvals", `"reset_approvals_on_push":true`, `"reset_approvals_on_push":false`},
		{"author approval", "/projects/41/approvals", `"merge_requests_author_approval":false`, `"merge_requests_author_approval":true`},
		{"overridable rules", "/projects/41/approvals", `"disable_overriding_approvers_per_merge_request":true`, `"disable_overriding_approvers_per_merge_request":false`},
		{"no required reviews", "/projects/41/approval_rules", `"approvals_required":1`, `"approvals_required":0`},
		{"hidden group", "/projects/41/approval_rules", `"contains_hidden_groups":false`, `"contains_hidden_groups":true`},
		{"overwritten MR rules", "/projects/41/merge_requests/7/approval_state", `"approval_rules_overwritten":false`, `"approval_rules_overwritten":true`},
		{"other project rule", "/projects/41/merge_requests/7/approval_state", `"source_rule":{"id":2}`, `"source_rule":{"id":99}`},
		{"unsatisfied rule", "/projects/41/merge_requests/7/approval_state", `"approved":true`, `"approved":false`},
		{"no approvals", "/projects/41/merge_requests/7/approvals", `"approved_by":[`, `"unexpected":[`},
		{"fork", "/projects/41/merge_requests/7", `"source_project_id":41`, `"source_project_id":99`},
		{"imported history", "/projects/41/merge_requests/7", `"id":101`, `"id":101,"imported":true`},
		{"wrong diff", "/projects/41/merge_requests/7/versions", strings.Repeat("b", 40), strings.Repeat("c", 40)},
		{"incomplete diff", "/projects/41/merge_requests/7/versions", `"state":"collected"`, `"state":"overflow"`},
		{"bot", "/users/11", `"bot":false`, `"bot":true`},
		{"unknown bot status", "/users/11", `,"bot":false`, ``},
		{"blocked reviewer", "/users/11", `"state":"active"`, `"state":"blocked"`},
		{"reader", "/projects/41/members/all/11", `"access_level":40`, `"access_level":20`},
		{"unknown role", "/projects/41/members/all/11", `"access_level":40`, `"access_level":999`},
		{"unknown expiry", "/projects/41/members/all/11", `"expires_at":null`, `"expires_at":"unknown"`},
		{"moved ref", "/projects/41/repository/branches/main", strings.Repeat("a", 40), strings.Repeat("d", 40)},
		{"repository transfer", "/projects/team/subgroup/shop", `"id":41`, `"id":42`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := reviewResponses()
			responses[tc.path] = strings.Replace(responses[tc.path], tc.old, tc.new, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, ok := responses[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				w.Write([]byte(body))
			}))
			defer server.Close()
			_, err := (Client{APIURL: server.URL}).ReviewedMergeEvidence(t.Context(), "secret", 41, "team/subgroup/shop", "main", strings.Repeat("a", 40))
			if err == nil {
				t.Fatal("unsafe or incomplete authority accepted")
			}
		})
	}
}

func TestReviewedGitLabMergeRejectsWildcardPushAndPolicyRace(t *testing.T) {
	for _, name := range []string{"wildcard", "unknown protection", "unsupported glob", "policy race", "eligibility race", "old review", "pagination"} {
		t.Run(name, func(t *testing.T) {
			responses := reviewResponses()
			reads, rulesRead := 0, 0
			if name == "wildcard" {
				responses["/projects/41/protected_branches"] = strings.TrimSuffix(responses["/projects/41/protected_branches"], "]") + `,{"id":2,"name":"m*","allow_force_push":false,"push_access_levels":[{"access_level":30}],"merge_access_levels":[{"access_level":40}]}]`
			}
			if name == "unknown protection" || name == "unsupported glob" {
				pattern := ""
				if name == "unsupported glob" {
					pattern = "m?in"
				}
				responses["/projects/41/protected_branches"] = strings.TrimSuffix(responses["/projects/41/protected_branches"], "]") + `,{"id":2,"name":"` + pattern + `","allow_force_push":false,"push_access_levels":[{"access_level":30}],"merge_access_levels":[{"access_level":40}]}]`
			}
			if name == "old review" {
				var x map[string]any
				json.Unmarshal([]byte(responses["/projects/41/merge_requests/7/approvals"]), &x)
				x["approved_by"].([]any)[0].(map[string]any)["approved_at"] = time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
				b, _ := json.Marshal(x)
				responses["/projects/41/merge_requests/7/approvals"] = string(b)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/projects/41/approvals" {
					reads++
					if name == "policy race" && reads == 2 {
						http.Error(w, "changed access", 403)
						return
					}
				}
				if r.URL.Path == "/projects/41/approval_rules" {
					rulesRead++
					if name == "eligibility race" && rulesRead == 2 {
						w.Write([]byte(strings.Replace(responses[r.URL.Path], `"protected_branches":[]`, `"protected_branches":[],"users":[{"id":99}]`, 1)))
						return
					}
				}
				if name == "pagination" && r.URL.Path == "/projects/41/protected_branches" {
					w.Header().Set("X-Next-Page", "9")
				}
				body, ok := responses[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				w.Write([]byte(body))
			}))
			defer server.Close()
			if _, err := (Client{APIURL: server.URL}).ReviewedMergeEvidence(t.Context(), "secret", 41, "team/subgroup/shop", "main", strings.Repeat("a", 40)); err == nil {
				t.Fatal("incomplete/racing policy qualified")
			}
		})
	}
}

func TestGitLabProvenanceBindsNumericProjectPathAndCommit(t *testing.T) {
	sha := strings.Repeat("a", 40)
	value := ProvenanceURL(41, "team/subgroup/shop", sha)
	if !MatchesProvenance(value, 41, "team/subgroup/shop", sha) {
		t.Fatal("valid provenance rejected")
	}
	for _, other := range []string{"github://team/subgroup/shop@" + sha, "gitlab://42/team/subgroup/shop@" + sha, "gitlab://41/team/other/shop@" + sha, value + "?token=secret", strings.Replace(value, sha, "main", 1)} {
		if MatchesProvenance(other, 41, "team/subgroup/shop", sha) {
			t.Fatal("foreign or mutable provenance accepted")
		}
	}
}
