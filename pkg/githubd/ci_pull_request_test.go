package githubd

import (
	"context"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func ciPullRequestJSON() string {
	return `{"number":42,"state":"open","draft":false,"head":{"sha":"` + strings.Repeat("a", 40) + `","repo":{"id":123,"full_name":"octo/api"}},"base":{"sha":"` + strings.Repeat("b", 40) + `","ref":"main","repo":{"id":123,"full_name":"octo/api"}}}`
}

func TestHTTPCIPullRequestRequiresCompleteBoundedIdentity(t *testing.T) {
	for _, mode := range []string{"valid", "closed", "draft", "fork", "absent_draft", "null_draft", "missing_id", "wrong_number", "unknown_state", "zero_sha", "duplicate", "case_alias", "unicode_case_alias", "kelvin_case_alias", "nested", "large", "trailing", "not_found", "provider_error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			body, responseStatus := ciPullRequestJSON(), http.StatusOK
			valid := mode == "valid" || mode == "closed" || mode == "draft" || mode == "fork"
			switch mode {
			case "closed":
				body = strings.Replace(body, `"state":"open"`, `"state":"closed"`, 1)
			case "draft":
				body = strings.Replace(body, `"draft":false`, `"draft":true`, 1)
			case "fork":
				body = strings.Replace(body, `"id":123,"full_name":"octo/api"`, `"id":999,"full_name":"foreign/fork"`, 1)
			case "absent_draft":
				body = strings.Replace(body, `"draft":false,`, "", 1)
			case "null_draft":
				body = strings.Replace(body, `"draft":false`, `"draft":null`, 1)
			case "missing_id":
				body = strings.Replace(body, `"id":123,`, "", 1)
			case "wrong_number":
				body = strings.Replace(body, `"number":42`, `"number":43`, 1)
			case "unknown_state":
				body = strings.Replace(body, `"state":"open"`, `"state":"unknown"`, 1)
			case "zero_sha":
				body = strings.Replace(body, strings.Repeat("a", 40), strings.Repeat("0", 40), 1)
			case "duplicate":
				body = `{"draft":true,` + body[1:]
			case "case_alias":
				body = `{"DRAFT":true,` + body[1:]
			case "unicode_case_alias":
				body = `{"\u017Ftate":"closed",` + body[1:]
			case "kelvin_case_alias":
				body = `{"extension":{"key":1,"\u212aEY":2},` + body[1:]
			case "nested":
				body = `{"extension":` + strings.Repeat("[", api.CIGitHubPullRequestJSONMaxDepth+1) + "0" + strings.Repeat("]", api.CIGitHubPullRequestJSONMaxDepth+1) + "," + body[1:]
			case "large":
				body += strings.Repeat(" ", api.CIGitHubPullRequestMaxBytes)
			case "trailing":
				body += `{}`
			case "not_found":
				responseStatus = http.StatusNotFound
			case "provider_error":
				responseStatus = http.StatusServiceUnavailable
				body = "private-provider-credential"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/repos/octo/api/pulls/42" || r.Header.Get("Authorization") != "Bearer private-install-token" {
					t.Error("PR lookup lost exact repository/number or installation authority")
				}
				w.WriteHeader(responseStatus)
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
				return "private-install-token", time.Now().Add(time.Hour), nil
			}), time.Minute)
			client := NewHTTPCIPullRequests(tokens, &singleHostClient{base: server.Client(), api: server.URL})
			ctx := t.Context()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			pr, err := client.CIPullRequest(ctx, 42, "octo/api", 42)
			if valid {
				if err != nil || !pr.Valid(42) || pr.BaseSHA != strings.Repeat("b", 40) || mode == "draft" && !pr.Draft || mode == "closed" && pr.State != "closed" || mode == "fork" && pr.HeadRepositoryID != 999 {
					t.Fatalf("valid current state lost: %+v %v", pr, err)
				}
			} else if err == nil || pr != (githubdgrpc.CIPullRequest{}) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("incomplete/provider response became authority or leaked details: %+v %v", pr, err)
			}
		})
	}
}

type ciPullRequestRecorder struct {
	calls int
	err   error
}

func (p *ciPullRequestRecorder) CIPullRequest(_ context.Context, _ int64, _ string, number int64) (githubdgrpc.CIPullRequest, error) {
	p.calls++
	pr, _ := decodeCIPullRequest([]byte(ciPullRequestJSON()), number)
	return pr, p.err
}

func TestCIPullRequestServiceOwnershipAndRecoveryDecorator(t *testing.T) {
	installs := newMemStoreInstalls()
	if err := installs.Upsert(t.Context(), state.GitHubInstall{AccountID: "owner", InstallationID: 42, AuditGithubLogin: "owner"}); err != nil {
		t.Fatal(err)
	}
	provider := &ciPullRequestRecorder{}
	service := &RealService{Installs: installs, CIPullRequests: provider}
	wrapped := NewRecoveryService(service, nil, nil)
	if _, err := wrapped.GetCIPullRequest(t.Context(), "neighbor", 42, "octo/api", 42); err == nil || provider.calls != 0 {
		t.Fatal("foreign account reached installation provider")
	}
	if pr, err := wrapped.GetCIPullRequest(t.Context(), "owner", 42, "octo/api", 42); err != nil || !pr.Valid(42) || provider.calls != 1 {
		t.Fatalf("production recovery wrapper hid the PR provider: %+v %v", pr, err)
	}
	provider.err = errors.New("private-provider-credential")
	if _, err := wrapped.GetCIPullRequest(t.Context(), "owner", 42, "octo/api", 42); err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatal("service leaked provider failure")
	}
	for _, repo := range []string{"", "../api", "octo/..", "octo/a/b", "octo/api?token=secret"} {
		if _, err := wrapped.GetCIPullRequest(t.Context(), "owner", 42, repo, 42); err == nil {
			t.Fatal("unsafe repository reached provider")
		}
	}
	if _, err := NewRecoveryService(githubdgrpc.UnimplementedService{}, nil, nil).GetCIPullRequest(t.Context(), "owner", 42, "octo/api", 42); status.Code(err) != codes.Unimplemented {
		t.Fatal("older service did not fail closed")
	}
}
