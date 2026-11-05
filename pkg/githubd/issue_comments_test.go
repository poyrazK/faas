// adr: 20261027090010
package githubd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/state"
)

// fakeIssue is one GitHub issue's comment thread.
type fakeIssue struct {
	mu       sync.Mutex
	comments []gitHubIssueComment
	nextID   int64
	writes   int
	// fail answers the next request of the method with that status; lose
	// applies the next write and then drops the connection.
	fail map[string]int
	lose bool
}

func (f *fakeIssue) add(body, userType string) {
	f.nextID++
	c := gitHubIssueComment{ID: f.nextID, Body: body, HTMLURL: fmt.Sprintf("https://github.com/acme/app/pull/7#issuecomment-%d", f.nextID)}
	c.User.Type = userType
	f.comments = append(f.comments, c)
}

func (f *fakeIssue) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer install-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if code := f.fail[r.Method]; code != 0 {
		delete(f.fail, r.Method)
		w.WriteHeader(code)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/issues/7/comments":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		from, to := min((page-1)*size, len(f.comments)), min(page*size, len(f.comments))
		_ = json.NewEncoder(w).Encode(f.comments[from:to])
	case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/issues/7/comments":
		f.writes++
		f.add(decodeBody(r), "Bot")
		f.answer(w, f.comments[len(f.comments)-1], http.StatusCreated)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/acme/app/issues/comments/"):
		f.writes++
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/repos/acme/app/issues/comments/"), 10, 64)
		for i := range f.comments {
			if f.comments[i].ID == id {
				f.comments[i].Body = decodeBody(r)
				f.answer(w, f.comments[i], http.StatusOK)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeIssue) answer(w http.ResponseWriter, c gitHubIssueComment, code int) {
	if f.lose {
		f.lose = false
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
		return
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(c)
}

func decodeBody(r *http.Request) string {
	var in struct {
		Body string `json:"body"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	return in.Body
}

func issueCommentsFixture(t *testing.T) (*fakeIssue, *RealService) {
	t.Helper()
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
	return issue, &RealService{Installs: installs, IssueComments: NewHTTPIssueComments(tokens, client)}
}

func TestUpsertIssueCommentKeepsOneCommentPerMarker(t *testing.T) {
	issue, svc := issueCommentsFixture(t)
	ctx := t.Context()
	upsert := func(body string) (int64, bool, error) {
		c, err := NewRecoveryService(svc, nil, nil).UpsertIssueComment(ctx, "owner", 42, "acme/app", 7, "agent-abc", body)
		return c.ID, c.Created, err
	}
	// A person quoting the marker does not capture the comment.
	issue.add("see <!-- gregale:agent-abc -->", "User")
	first, created, err := upsert("first")
	if err != nil || !created || len(issue.comments) != 2 || issue.comments[1].Body != "first\n\n<!-- gregale:agent-abc -->" {
		t.Fatalf("create: %d %v %v %+v", first, created, err, issue.comments)
	}
	second, created, err := upsert("second")
	if err != nil || created || second != first || len(issue.comments) != 2 || !strings.HasPrefix(issue.comments[1].Body, "second") {
		t.Fatalf("update: %d %v %v %+v", second, created, err, issue.comments)
	}
	writes := issue.writes
	if _, _, err := upsert("second"); err != nil || issue.writes != writes {
		t.Fatalf("an unchanged comment was written again: %v writes %d", err, issue.writes-writes)
	}
	if _, err := NewRecoveryService(svc, nil, nil).UpsertIssueComment(ctx, "owner", 42, "acme/app", 7, "agent-other", "other"); err != nil || len(issue.comments) != 3 {
		t.Fatalf("another marker: %v %d", err, len(issue.comments))
	}
}

func TestUpsertIssueCommentFindsTheMarkerOnALaterPage(t *testing.T) {
	issue, svc := issueCommentsFixture(t)
	issue.add("old\n\n<!-- gregale:agent-abc -->", "Bot")
	for range issueCommentPageSize + 5 {
		issue.add("chatter", "User")
	}
	issue.comments = append(issue.comments[1:], issue.comments[0])
	c, err := svc.UpsertIssueComment(t.Context(), "owner", 42, "acme/app", 7, "agent-abc", "new")
	if err != nil || c.Created || c.ID != 1 || len(issue.comments) != issueCommentPageSize+6 {
		t.Fatalf("%+v %v %d", c, err, len(issue.comments))
	}
}

// A write whose answer is lost is confirmed by reading the issue again.
func TestUpsertIssueCommentReconcilesALostAnswer(t *testing.T) {
	issue, svc := issueCommentsFixture(t)
	issue.lose = true
	c, err := svc.UpsertIssueComment(t.Context(), "owner", 42, "acme/app", 7, "agent-abc", "hi")
	if err != nil || !c.Created || c.ID != 1 || len(issue.comments) != 1 {
		t.Fatalf("lost create: %+v %v %+v", c, err, issue.comments)
	}
	// A server error on the write that did not land leaves nothing to
	// confirm: the caller may retry.
	issue.fail[http.MethodPatch] = http.StatusBadGateway
	_, err = svc.UpsertIssueComment(t.Context(), "owner", 42, "acme/app", 7, "agent-abc", "changed")
	if status.Code(err) != codes.Unavailable || !strings.HasPrefix(issue.comments[0].Body, "hi") {
		t.Fatalf("failed update: %v %+v", err, issue.comments)
	}
}

func TestUpsertIssueCommentClassifiesGitHub(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		code   int
		want   codes.Code
	}{
		"listing unavailable": {http.MethodGet, http.StatusServiceUnavailable, codes.Unavailable},
		"rate limited":        {http.MethodPost, http.StatusTooManyRequests, codes.Unavailable},
		"no access":           {http.MethodPost, http.StatusForbidden, codes.FailedPrecondition},
		"locked":              {http.MethodPost, http.StatusUnprocessableEntity, codes.FailedPrecondition},
		"missing issue":       {http.MethodGet, http.StatusNotFound, codes.FailedPrecondition},
	} {
		issue, svc := issueCommentsFixture(t)
		issue.fail[tc.method] = tc.code
		if _, err := svc.UpsertIssueComment(t.Context(), "owner", 42, "acme/app", 7, "agent-abc", "hi"); status.Code(err) != tc.want {
			t.Errorf("%s: %v, want %s", name, err, tc.want)
		}
		if len(issue.comments) != 0 {
			t.Errorf("%s: wrote %+v", name, issue.comments)
		}
	}
}

func TestUpsertIssueCommentChecksTheRequest(t *testing.T) {
	issue, svc := issueCommentsFixture(t)
	ctx := t.Context()
	for name, tc := range map[string]struct {
		account, repo, marker, body string
		installation, number        int64
		want                        codes.Code
	}{
		"foreign account":      {"neighbor", "acme/app", "agent-abc", "hi", 42, 7, codes.NotFound},
		"foreign installation": {"owner", "acme/app", "agent-abc", "hi", 43, 7, codes.NotFound},
		"path in repository":   {"owner", "acme/../x", "agent-abc", "hi", 42, 7, codes.InvalidArgument},
		"no number":            {"owner", "acme/app", "agent-abc", "hi", 42, 0, codes.InvalidArgument},
		"marker breaks out":    {"owner", "acme/app", "x --> <script>", "hi", 42, 7, codes.InvalidArgument},
		"empty body":           {"owner", "acme/app", "agent-abc", " ", 42, 7, codes.InvalidArgument},
		"body over the limit":  {"owner", "acme/app", "agent-abc", strings.Repeat("a", issueCommentBodyMaxBytes), 42, 7, codes.InvalidArgument},
	} {
		if _, err := svc.UpsertIssueComment(ctx, tc.account, tc.installation, tc.repo, tc.number, tc.marker, tc.body); status.Code(err) != tc.want {
			t.Errorf("%s: %v, want %s", name, err, tc.want)
		}
	}
	if issue.writes != 0 || len(issue.comments) != 0 {
		t.Fatalf("a refused request reached GitHub: %d", issue.writes)
	}
}
