// adr: 20261101160300
package githubd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type accountRename struct {
	id       int64
	from, to string
}

type accountRenameRecorder struct {
	lifecycleRecorder
	accounts []accountRename
}

func (r *accountRenameRecorder) RenameGitHubAccount(_ context.Context, id int64, oldLogin, newLogin string) error {
	r.accounts = append(r.accounts, accountRename{id, oldLogin, newLogin})
	return nil
}

// GitHub reports a renamed user or organization with installation_target
// and no repository event, so every binding kept the old owner and no push
// matched it.
func TestHandleInstallationTargetRenameMovesBindings(t *testing.T) {
	recorder := &accountRenameRecorder{}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Lifecycle = recorder
	var invalidated []int64
	svc.InvalidateInstallationBindings = func(id int64) { invalidated = append(invalidated, id) }
	body := `{"action":"renamed","installation":{"id":42},"account":{"login":"acme-inc"},"changes":{"login":{"from":"acme"}},"target_type":"Organization"}`
	if err := svc.HandleWebhookEvent(context.Background(), "installation_target", []byte(body)); err != nil {
		t.Fatal(err)
	}
	if want := []accountRename{{42, "acme", "acme-inc"}}; !reflect.DeepEqual(recorder.accounts, want) {
		t.Fatalf("account renames = %v, want %v", recorder.accounts, want)
	}
	if !reflect.DeepEqual(invalidated, []int64{42}) {
		t.Fatalf("invalidated = %v, want the installation's bindings", invalidated)
	}
	// Other actions, and a rename without the old login, change nothing.
	for _, other := range []string{
		`{"action":"renamed","installation":{"id":42},"account":{"login":"acme-inc"},"changes":{}}`,
		`{"action":"updated","installation":{"id":42},"account":{"login":"acme"},"changes":{"login":{"from":"x"}}}`,
	} {
		if err := svc.HandleWebhookEvent(context.Background(), "installation_target", []byte(other)); err != nil {
			t.Fatal(err)
		}
	}
	if len(recorder.accounts) != 1 {
		t.Fatalf("account renames = %v, want only the first", recorder.accounts)
	}
}

type renamingSyncStore struct {
	syncStoreFake
	renamed [][2]string
}

func (f *renamingSyncStore) RenameGitHubRepository(_ context.Context, _ int64, oldName, newName string) error {
	f.renamed = append(f.renamed, [2]string{oldName, newName})
	return nil
}

// A rename the repository webhook missed detached the binding at the next
// sync although GitHub still listed the same repository ID.
func TestInstallationSyncerRenamesBindingsByRepositoryID(t *testing.T) {
	store := &renamingSyncStore{syncStoreFake: syncStoreFake{
		installs: []state.GitHubInstall{{AccountID: "acct-1", InstallationID: 42}},
		bindings: map[int64][]state.GitHubBinding{42: {
			{RepoFullName: "acme/api", RepoID: 7},
			{RepoFullName: "acme/gone", RepoID: 8},
		}},
		bindingErr: map[int64]error{}, removeErr: map[int64]error{}, recordErr: map[int64]error{},
	}}
	client := &syncClientFake{byInstall: map[int64][]githubdgrpc.Repo{42: {{ID: 7, FullName: "acme-inc/api"}}}, errs: map[int64]error{}}
	summary, err := NewInstallationSyncer(store, client).SyncOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.renamed, [][2]string{{"acme/api", "acme-inc/api"}}) {
		t.Fatalf("renamed = %v", store.renamed)
	}
	if !reflect.DeepEqual(store.removed[42], []string{"acme/gone"}) || summary.RenamedBindings != 1 || summary.DetachedBindings != 1 {
		t.Fatalf("removed = %v, summary = %+v", store.removed[42], summary)
	}
}

func TestAppConfigGapsNameMissingEventsAndPermissions(t *testing.T) {
	// The production App on 2026-11-01: push and pull_request only.
	config := AppConfig{
		Events: []string{"pull_request", "push"},
		Permissions: map[string]string{"checks": "write", "contents": "read", "deployments": "write",
			"issues": "write", "members": "read", "metadata": "read", "pull_requests": "read"},
	}
	want := []AppConfigGap{{Kind: "event", Name: "issues"}, {Kind: "event", Name: "repository"}}
	if got := config.Gaps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %v, want %v", got, want)
	}
	config.Events = append(config.Events, "issues", "repository")
	config.Permissions["checks"] = "read"
	delete(config.Permissions, "members")
	want = []AppConfigGap{{Kind: "permission", Name: "checks"}, {Kind: "permission", Name: "members"}}
	if got := config.Gaps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("gaps = %v, want %v", got, want)
	}
}

func TestHookDeliveryMonitorExportsAppConfigGaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"events":      []string{"push", "pull_request"},
				"permissions": RequiredAppPermissions,
			})
		case "/app/hook/deliveries":
			_ = json.NewEncoder(w).Encode([]HookDelivery{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	metrics := NewAppConfigMetrics()
	m := &HookDeliveryMonitor{
		MintJWT: func() (string, error) { return "app-jwt", nil }, Client: srv.Client(), BaseURL: srv.URL,
		Metrics: NewHookDeliveryMetrics(), AppConfigMetrics: metrics,
	}
	if _, err := m.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := `
# HELP githubd_github_app_config_missing 1 when the GitHub App lacks a webhook event or permission docs/ops/github-app.md requires.
# TYPE githubd_github_app_config_missing gauge
githubd_github_app_config_missing{kind="event",name="issues"} 1
githubd_github_app_config_missing{kind="event",name="pull_request"} 0
githubd_github_app_config_missing{kind="event",name="push"} 0
githubd_github_app_config_missing{kind="event",name="repository"} 1
githubd_github_app_config_missing{kind="permission",name="checks"} 0
githubd_github_app_config_missing{kind="permission",name="contents"} 0
githubd_github_app_config_missing{kind="permission",name="deployments"} 0
githubd_github_app_config_missing{kind="permission",name="issues"} 0
githubd_github_app_config_missing{kind="permission",name="members"} 0
githubd_github_app_config_missing{kind="permission",name="metadata"} 0
githubd_github_app_config_missing{kind="permission",name="pull_requests"} 0
`
	if err := testutil.CollectAndCompare(metrics, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}
