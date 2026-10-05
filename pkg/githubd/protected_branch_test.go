package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

const reviewedBranchPolicy = `{"enforce_admins":{"enabled":true},"allow_force_pushes":{"enabled":false},"allow_deletions":{"enabled":false},"required_pull_request_reviews":{"required_approving_review_count":1,"dismiss_stale_reviews":true,"require_last_push_approval":true,"bypass_pull_request_allowances":{"users":[],"teams":[],"apps":[]}}}`

func TestHTTPProtectedBranchEvidence(t *testing.T) {
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name, reason string
		mutate       func(map[string]any)
		status       int
		headChange   bool
		policyChange bool
		repositoryID int64
		oversized    bool
	}{
		{name: "qualified"},
		{name: "admins can bypass", reason: "admins_not_enforced", mutate: func(p map[string]any) { p["enforce_admins"] = map[string]any{"enabled": false} }},
		{name: "force push", reason: "force_push_allowed", mutate: func(p map[string]any) { p["allow_force_pushes"] = map[string]any{"enabled": true} }},
		{name: "deletion", reason: "branch_deletion_allowed", mutate: func(p map[string]any) { p["allow_deletions"] = map[string]any{"enabled": true} }},
		{name: "no reviews", reason: "reviews_not_required", mutate: func(p map[string]any) { p["required_pull_request_reviews"] = nil }},
		{name: "stale reviews", reason: "stale_reviews_allowed", mutate: func(p map[string]any) {
			p["required_pull_request_reviews"].(map[string]any)["dismiss_stale_reviews"] = false
		}},
		{name: "last push", reason: "last_push_approval_not_required", mutate: func(p map[string]any) {
			p["required_pull_request_reviews"].(map[string]any)["require_last_push_approval"] = false
		}},
		{name: "bypass app", reason: "review_bypass_allowed", mutate: func(p map[string]any) {
			p["required_pull_request_reviews"].(map[string]any)["bypass_pull_request_allowances"] = map[string]any{"apps": []any{map[string]any{"id": 1}}}
		}},
		{name: "missing mandatory switch", reason: "protection_incomplete", mutate: func(p map[string]any) { delete(p, "allow_force_pushes") }},
		{name: "ruleset only", reason: "classic_protection_unavailable", status: http.StatusNotFound},
		{name: "permissions unavailable", status: http.StatusForbidden},
		{name: "provider outage", status: http.StatusServiceUnavailable},
		{name: "head advances", reason: "branch_head_changed", headChange: true},
		{name: "policy changes", reason: "branch_protection_changed", policyChange: true},
		{name: "repository recreated", reason: "repository_identity_mismatch", repositoryID: 999},
		{name: "oversized success", oversized: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var policy map[string]any
			if err := json.Unmarshal([]byte(reviewedBranchPolicy), &policy); err != nil {
				t.Fatal(err)
			}
			if tc.mutate != nil {
				tc.mutate(policy)
			}
			heads, protections := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer private-install-token" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
					t.Errorf("invalid provider request: %s", r.Method)
				}
				switch r.URL.EscapedPath() {
				case "/repos/octo/api":
					id := int64(123)
					if tc.repositoryID != 0 {
						id = tc.repositoryID
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "full_name": "octo/api"})
				case "/repos/octo/api/branches/release%2Fcanary":
					heads++
					commit := sha
					if tc.headChange && heads == 2 {
						commit = strings.Repeat("b", 40)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"name": "release/canary", "protected": true, "commit": map[string]any{"sha": commit}})
				case "/repos/octo/api/branches/release%2Fcanary/protection":
					protections++
					if tc.status != 0 {
						w.WriteHeader(tc.status)
						_, _ = io.WriteString(w, `{"message":"private-install-token"}`)
						return
					}
					if tc.policyChange && protections == 2 {
						policy["required_pull_request_reviews"].(map[string]any)["required_approving_review_count"] = 2
					}
					_ = json.NewEncoder(w).Encode(policy)
					if tc.oversized {
						_, _ = io.WriteString(w, strings.Repeat(" ", githubResponseMaxBytes))
					}
				default:
					t.Errorf("unexpected request: %s", r.URL.EscapedPath())
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
				return "private-install-token", time.Now().Add(time.Hour), nil
			}), time.Minute)
			client := NewHTTPProtectedBranches(tokens, &singleHostClient{base: server.Client(), api: server.URL})
			evidence, err := client.ProtectedBranchEvidence(t.Context(), 42, 123, "octo/api", "release/canary", sha)
			switch {
			case tc.name == "qualified":
				if err != nil || !evidence.ValidFor(42, 123, "octo/api", "release/canary", sha, time.Now(), api.EnvironmentGitProtectedBranchEvidenceMaxAge) || heads != 2 || protections != 2 {
					t.Fatalf("qualified observation: %+v %v heads=%d policies=%d", evidence, err, heads, protections)
				}
			case tc.reason != "":
				if err != nil || evidence.Qualified || evidence.Reason != tc.reason || evidence.PolicyDigest != "" || !evidence.CheckedAt.IsZero() {
					t.Fatalf("rejected observation: %+v %v", evidence, err)
				}
			default:
				if !errors.Is(err, ErrProtectedBranchEvidenceUnavailable) || evidence.Qualified || strings.Contains(err.Error(), "private-install-token") {
					t.Fatalf("unavailable observation exposed credentials or qualified: %+v %v", evidence, err)
				}
			}
		})
	}
}

type protectedBranchRecorder struct{ calls int }

func (p *protectedBranchRecorder) ProtectedBranchEvidence(context.Context, int64, int64, string, string, string) (githubdgrpc.ProtectedBranchEvidence, error) {
	p.calls++
	return githubdgrpc.ProtectedBranchEvidence{Reason: "branch_unprotected"}, nil
}

func TestProtectedBranchEvidenceServiceRequiresAccountInstallation(t *testing.T) {
	installs := newMemStoreInstalls()
	if err := installs.Upsert(t.Context(), state.GitHubInstall{AccountID: "owner", InstallationID: 42, AuditGithubLogin: "owner"}); err != nil {
		t.Fatal(err)
	}
	provider := &protectedBranchRecorder{}
	service := &RealService{Installs: installs, ProtectedBranches: provider}
	for _, account := range []string{"neighbor", "owner"} {
		evidence, err := service.GetProtectedBranchEvidence(t.Context(), account, 42, 123, "octo/api", "main", strings.Repeat("a", 40))
		if account == "neighbor" && (err == nil || provider.calls != 0) {
			t.Fatalf("foreign account accessed provider: %+v %v calls=%d", evidence, err, provider.calls)
		}
		if account == "owner" && (err != nil || provider.calls != 1 || evidence.Qualified) {
			t.Fatalf("owner lookup: %+v %v calls=%d", evidence, err, provider.calls)
		}
	}
}

func TestHTTPProtectedBranchEvidenceRejectsInvalidInputsAndCancellation(t *testing.T) {
	fetches := 0
	tokens := NewTokenCache(fakeFetcher(func(ctx context.Context, _ int64) (string, time.Time, error) {
		fetches++
		return "", time.Time{}, ctx.Err()
	}), time.Minute)
	client := NewHTTPProtectedBranches(tokens, nil)
	for _, branch := range []string{"", "main..old", "main~1", "main/../next"} {
		evidence, err := client.ProtectedBranchEvidence(t.Context(), 42, 123, "octo/api", branch, strings.Repeat("a", 40))
		if err == nil || evidence.Qualified || fetches != 0 {
			t.Fatalf("invalid ref reached credentials: %+v %v fetches=%d", evidence, err, fetches)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	evidence, err := client.ProtectedBranchEvidence(ctx, 42, 123, "octo/api", "main", strings.Repeat("a", 40))
	if !errors.Is(err, ErrProtectedBranchEvidenceUnavailable) || evidence.Qualified {
		t.Fatalf("cancelled observation: %+v %v", evidence, err)
	}
}
