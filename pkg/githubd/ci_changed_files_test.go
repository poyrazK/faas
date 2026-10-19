package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func ciComparisonJSON() string {
	return `{"status":"ahead","total_commits":1,"base_commit":{"sha":"` + strings.Repeat("a", 40) + `"},"merge_base_commit":{"sha":"` + strings.Repeat("a", 40) + `"},"commits":[{"sha":"` + strings.Repeat("b", 40) + `"}],"files":[{"filename":"docs/readme","status":"modified"}]}`
}

func TestHTTPCIChangedFilesBoundedPinnedAndComplete(t *testing.T) {
	for _, mode := range []string{"valid", "renamed", "removed", "diverged", "empty", "identical", "missing_files", "null_files", "missing_count", "missing_base", "wrong_base", "wrong_head", "missing_merge_base", "unknown_status", "missing_filename", "missing_previous", "duplicate", "case_alias", "unicode_alias", "invalid_utf8", "surrogate", "nested", "trailing", "large", "file_cap", "commit_cap", "paginated", "behind", "not_found", "provider_error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			body, statusCode := ciComparisonJSON(), http.StatusOK
			base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
			valid := mode == "valid" || mode == "renamed" || mode == "removed" || mode == "diverged" || mode == "empty" || mode == "identical"
			switch mode {
			case "renamed":
				body = strings.Replace(body, `"filename":"docs/readme","status":"modified"`, `"filename":"docs/readme","previous_filename":"src/main.go","status":"renamed"`, 1)
			case "removed":
				body = strings.Replace(body, `"modified"`, `"removed"`, 1)
			case "diverged":
				body = strings.Replace(body, `"ahead"`, `"diverged"`, 1)
			case "empty":
				body = strings.Replace(body, `[{"filename":"docs/readme","status":"modified"}]`, `[]`, 1)
			case "identical":
				head = base
				body = `{"status":"identical","total_commits":0,"base_commit":{"sha":"` + base + `"},"merge_base_commit":{"sha":"` + base + `"},"commits":[],"files":[]}`
			case "missing_files":
				body = strings.Replace(body, `,"files":[{"filename":"docs/readme","status":"modified"}]`, "", 1)
			case "null_files":
				body = strings.Replace(body, `[{"filename":"docs/readme","status":"modified"}]`, `null`, 1)
			case "missing_count":
				body = strings.Replace(body, `"total_commits":1,`, "", 1)
			case "missing_base":
				body = strings.Replace(body, `"base_commit":{"sha":"`+base+`"},`, "", 1)
			case "wrong_base":
				body = strings.Replace(body, base, strings.Repeat("c", 40), 1)
			case "wrong_head":
				body = strings.Replace(body, head, strings.Repeat("c", 40), 1)
			case "missing_merge_base":
				body = strings.Replace(body, `"merge_base_commit":{"sha":"`+base+`"},`, "", 1)
			case "unknown_status":
				body = strings.Replace(body, `"modified"`, `"future"`, 1)
			case "missing_filename":
				body = strings.Replace(body, `"filename":"docs/readme",`, "", 1)
			case "missing_previous":
				body = strings.Replace(body, `"modified"`, `"renamed"`, 1)
			case "duplicate":
				body = `{"files":[],` + body[1:]
			case "case_alias":
				body = `{"FILES":[],` + body[1:]
			case "unicode_alias":
				body = `{"\u017Ftatus":"behind",` + body[1:]
			case "invalid_utf8":
				body = strings.Replace(body, "docs/readme", "docs/"+string([]byte{0xff}), 1)
			case "surrogate":
				body = strings.Replace(body, "docs/readme", `docs/\ud800`, 1)
			case "nested":
				body = `{"extension":` + strings.Repeat("[", api.CIGitHubPullRequestJSONMaxDepth+1) + "0" + strings.Repeat("]", api.CIGitHubPullRequestJSONMaxDepth+1) + "," + body[1:]
			case "trailing":
				body += `{}`
			case "large":
				body += strings.Repeat(" ", api.CIAutomationCompareMaxBytes)
			case "file_cap":
				files := make([]map[string]string, api.CIAutomationCompareMaxFiles)
				for i := range files {
					files[i] = map[string]string{"filename": "docs/" + strconv.Itoa(i), "status": "modified"}
				}
				encoded, _ := json.Marshal(files)
				body = strings.Replace(body, `[{"filename":"docs/readme","status":"modified"}]`, string(encoded), 1)
			case "commit_cap":
				body = strings.Replace(body, `"total_commits":1`, `"total_commits":100`, 1)
				commits := make([]map[string]string, api.CIAutomationCompareMaxCommits)
				for i := range commits {
					commits[i] = map[string]string{"sha": strings.Repeat("c", 40)}
				}
				commits[len(commits)-1]["sha"] = head
				encoded, _ := json.Marshal(commits)
				body = strings.Replace(body, `[{"sha":"`+head+`"}]`, string(encoded), 1)
			case "behind":
				body = strings.Replace(body, `"ahead"`, `"behind"`, 1)
			case "not_found":
				statusCode = http.StatusNotFound
			case "provider_error":
				statusCode = http.StatusBadGateway
				body = "private-provider-token"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/repos/octo/api/compare/"+base+"..."+head || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("page") != "1" || r.Header.Get("Authorization") != "Bearer private-install-token" {
					t.Error("comparison lost exact pins or installation scope")
				}
				if mode == "paginated" {
					w.Header().Set("Link", `<https://api.github.com/repos/octo/api/compare/mutable?attack=1>; rel="next"`)
				}
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
				return "private-install-token", time.Now().Add(time.Hour), nil
			}), time.Minute)
			client := NewHTTPCIChangedFiles(tokens, &singleHostClient{base: server.Client(), api: server.URL})
			ctx := t.Context()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			changes, err := client.CIChangedFiles(ctx, 42, "octo/api", base, head)
			if valid {
				if err != nil || !changes.Valid(base, head) {
					t.Fatalf("valid comparison lost: %+v %v", changes, err)
				}
			} else if err == nil || changes.Complete || strings.Contains(err.Error(), "private-") {
				t.Fatalf("unsafe comparison became skip evidence: %+v %v", changes, err)
			}
		})
	}
}

type ciChangesRecorder struct {
	calls int
	err   error
}

func (p *ciChangesRecorder) CIChangedFiles(_ context.Context, _ int64, _, base, head string) (ci.ChangedFiles, error) {
	p.calls++
	return ci.ChangedFiles{BaseSHA: base, HeadSHA: head, MergeBaseSHA: base, Complete: true}, p.err
}

func TestCIChangedFilesOwnershipAndRecoveryDecorator(t *testing.T) {
	installs := newMemStoreInstalls()
	if err := installs.Upsert(t.Context(), state.GitHubInstall{AccountID: "owner", InstallationID: 42, AuditGithubLogin: "owner"}); err != nil {
		t.Fatal(err)
	}
	provider := &ciChangesRecorder{}
	wrapped := NewRecoveryService(&RealService{Installs: installs, CIChanges: provider}, nil, nil)
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	if _, err := wrapped.GetCIChangedFiles(t.Context(), "neighbor", 42, "octo/api", base, head); err == nil || provider.calls != 0 {
		t.Fatal("foreign account reached comparison provider")
	}
	if got, err := wrapped.GetCIChangedFiles(t.Context(), "owner", 42, "octo/api", base, head); err != nil || !got.Valid(base, head) || provider.calls != 1 {
		t.Fatal("wrapper hid comparison capability")
	}
	provider.err = errors.New("private-provider-token")
	if _, err := wrapped.GetCIChangedFiles(t.Context(), "owner", 42, "octo/api", base, head); err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatal("provider detail leaked")
	}
	for _, repo := range []string{"../api", "octo/..", "octo/api?token=secret", "octo/a/b"} {
		if _, err := wrapped.GetCIChangedFiles(t.Context(), "owner", 42, repo, base, head); err == nil {
			t.Fatal("unsafe repository reached provider")
		}
	}
	for _, pin := range []string{"refs/heads/main", strings.Repeat("0", 40), "short"} {
		if _, err := wrapped.GetCIChangedFiles(t.Context(), "owner", 42, "octo/api", pin, head); err == nil {
			t.Fatal("mutable or invalid comparison pin accepted")
		}
	}
	if _, err := NewRecoveryService(githubdgrpc.UnimplementedService{}, nil, nil).GetCIChangedFiles(t.Context(), "owner", 42, "octo/api", base, head); status.Code(err) != codes.Unimplemented {
		t.Fatal("older service did not fail closed")
	}
}

func TestCISkippedCheckLostCreateAcknowledgementRecoversIdentity(t *testing.T) {
	check := executionCICheckFixture()
	check.ExecutionID = ""
	check.AutomationEventID = uuid.NewString()
	check.Status, check.Conclusion = "completed", "skipped"
	check.Summary = "CI skipped by path filters; no compute execution was created."
	creates, patches := 0, 0
	var remote map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if strings.Contains(r.URL.Path, "/commits/") {
				checks := []any{}
				if remote != nil {
					checks = append(checks, remote)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": checks})
			} else {
				_ = json.NewEncoder(w).Encode(remote)
			}
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["external_id"] != check.ExternalID() || payload["conclusion"] != "skipped" {
			t.Error("skip Check reported execution/success identity")
		}
		if r.Method == http.MethodPost {
			creates++
			remote = payload
			remote["id"] = 77
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{`)
			return
		}
		patches++
		_, _ = io.WriteString(w, `{"id":77}`)
	}))
	defer server.Close()
	tokens := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
		return "synthetic-ci-token", time.Now().Add(time.Hour), nil
	}), time.Minute)
	writer := &ChecksAPI{Tokens: tokens, HTTP: &singleHostClient{base: server.Client(), api: server.URL}}
	if _, err := writer.WriteExecutionCICheck(t.Context(), check); err == nil {
		t.Fatal("malformed acknowledgement treated as delivered")
	}
	if id, err := writer.WriteExecutionCICheck(t.Context(), check); err != nil || id != 77 || creates != 1 || patches != 1 {
		t.Fatalf("lost ack duplicated skipped Check: %d %d %v", creates, patches, err)
	}
	check.ExecutionID = uuid.NewString()
	if check.Valid() {
		t.Fatal("source and execution identities could be mixed")
	}
}
