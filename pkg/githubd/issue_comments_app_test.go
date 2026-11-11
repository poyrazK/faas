package githubd

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestUpsertIssueCommentIgnoresAnotherAppsBotComment covers
// ADR-20261105090226: a pull request's workflow comments as
// github-actions[bot], so a bot comment carrying the marker is adopted only
// when GitHub attributes it to this App.
func TestUpsertIssueCommentIgnoresAnotherAppsBotComment(t *testing.T) {
	issue := &fakeIssue{fail: map[string]int{}}
	server := httptest.NewServer(issue)
	t.Cleanup(server.Close)
	tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
		return "install-token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	installs := newMemStoreInstalls()
	if err := installs.Upsert(t.Context(), state.GitHubInstall{AccountID: "owner", InstallationID: 42, AuditGithubLogin: "owner"}); err != nil {
		t.Fatal(err)
	}
	client := &singleHostClient{base: server.Client(), api: server.URL}
	svc := &RealService{Installs: installs, IssueComments: NewHTTPIssueComments(tokens, client, 1001)}
	issue.add("planted <!-- gregale:agent-abc -->", "Bot")
	issue.comments[0].PerformedViaGitHubApp = &struct {
		ID int64 `json:"id"`
	}{ID: 15368}
	written, err := NewRecoveryService(svc, nil, nil).UpsertIssueComment(t.Context(), "owner", 42, "acme/app", 7, "agent-abc", "status")
	if err != nil || !written.Created || len(issue.comments) != 2 || !strings.HasPrefix(issue.comments[0].Body, "planted") {
		t.Fatalf("another App's comment was adopted: %+v %v %+v", written, err, issue.comments)
	}
}
