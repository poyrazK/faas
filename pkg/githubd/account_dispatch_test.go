package githubd

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

type sharedInstallationBindings struct{ bindings []state.GitHubBinding }

func (s sharedInstallationBindings) ListBindingsForInstallation(context.Context, int64) ([]state.GitHubBinding, error) {
	return s.bindings, nil
}

type sharedMemBindings struct {
	memBindings
	accounts []string
}

func (b sharedMemBindings) ListBindingsForInstallation(ctx context.Context, installationID int64) ([]state.GitHubBinding, error) {
	var result []state.GitHubBinding
	for _, accountID := range b.accounts {
		bindings, err := b.ListForAccount(ctx, accountID)
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			if binding.InstallID == installationID {
				result = append(result, binding)
			}
		}
	}
	return result, nil
}

func TestSharedInstallationPushKeepsEveryAppAndLiveBindingChecks(t *testing.T) {
	ctx := context.Background()
	service, rec, rig := appBoundRig(t)
	other, err := rig.mem.CreateAccount(ctx, "shared-apps@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	otherRig := &testRig{mem: rig.mem, acct: other.ID, install: 42}
	first := rig.bindApp(t, "first-api", "", "octo/api", "main")
	second := rig.bindApp(t, "first-worker", "", "octo/api", "main")
	third := otherRig.bindApp(t, "second-api", "", "octo/api", "main")
	fourth := otherRig.bindApp(t, "second-worker", "", "octo/api", "main")
	source := sharedMemBindings{memBindings{mem: rig.mem, acct: rig.acct}, []string{rig.acct, other.ID}}
	service.Bindings = source
	service.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{rig.acct: {AccountID: rig.acct, InstallationID: 42}, other.ID: {AccountID: other.ID, InstallationID: 42}}}
	if _, err := service.HandlePushRequest(ctx, []byte(appBoundPush)); err != nil {
		t.Fatal(err)
	}
	want := []string{first.ID, second.ID, third.ID, fourth.ID}
	slices.Sort(want)
	if !slices.Equal(enqueuedApps(rec), want) {
		t.Fatalf("enqueued %v want %v", enqueuedApps(rec), want)
	}
	initial, err := source.ListForAccount(ctx, rig.acct)
	if err != nil {
		t.Fatal(err)
	}
	scoped := accountEventBindings{source: source, accountID: rig.acct}
	for _, binding := range initial {
		scoped.bindings = append(scoped.bindings, binding)
	}
	if _, err := scoped.GetAppBinding(ctx, "octo/api", "main"); err != nil {
		t.Fatalf("live binding missing before revoke: %v", err)
	}
	if _, err := scoped.ListForAccount(ctx, other.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account listing: %v", err)
	}
	if err := rig.mem.DeleteGithubInstallBinding(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := rig.mem.DeleteGithubInstallBinding(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.GetAppBinding(ctx, "octo/api", "main"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("revoked bindings retained: %v", err)
	}
}

func TestSharedInstallationPullRequestCreatesSeparatePreviews(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	other, err := rig.mem.CreateAccount(ctx, "shared-previews@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	project, err := rig.mem.CreateProject(ctx, state.Project{AccountID: other.ID, Slug: "second-project"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := rig.mem.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "second-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive, ProjectID: project.ID, WorkloadName: "api"})
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []state.GitHubBinding{
		{AccountID: rig.acct, AppID: rig.parentID, BindingID: "first", InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main"},
		{AccountID: other.ID, AppID: parent.ID, BindingID: "second", InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main"},
	} {
		if err := rig.mem.UpsertGithubInstallBinding(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	service, _ := newPreviewService(t, rig)
	service.Bindings = sharedMemBindings{memBindings{mem: rig.mem, acct: rig.acct}, []string{rig.acct, other.ID}}
	service.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{rig.acct: {AccountID: rig.acct, InstallationID: 42}, other.ID: {AccountID: other.ID, InstallationID: 42}}}
	if _, err := service.handlePullRequest(ctx, pullRequestOpenedBody(17, strings.Repeat("a", 40))); err != nil {
		t.Fatal(err)
	}
	for _, accountID := range []string{rig.acct, other.ID} {
		apps, err := rig.mem.ListApps(ctx, accountID)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, app := range apps {
			if app.PreviewPrNumber == 17 {
				count++
				if app.AccountID != accountID {
					t.Fatal("preview changed account")
				}
			}
		}
		if count != 1 {
			t.Fatalf("account %s has %d previews, want one", accountID, count)
		}
	}
}
func (s sharedInstallationBindings) GetAppBinding(context.Context, string, string) (state.GitHubBinding, error) {
	return state.GitHubBinding{}, errors.New("unscoped lookup must not run")
}

func TestSharedInstallationDispatchScopesDeduplicatesAndContinues(t *testing.T) {
	bindings := []state.GitHubBinding{
		{AccountID: "a", InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main", AppID: "a-one"},
		{AccountID: "a", InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main", AppID: "a-two"},
		{AccountID: "b", InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main", AppID: "b-one"},
		{AccountID: "other-install", InstallID: 99, RepoFullName: "octo/api"},
		{AccountID: "other-repo", InstallID: 42, RepoFullName: "octo/private"},
	}
	service := &Service{Bindings: sharedInstallationBindings{bindings}}
	failure := errors.New("account a failed")
	var calls []string
	result, err := service.dispatchRepositoryAccounts(context.Background(), 42, "octo/api", func(ctx context.Context, scoped *Service) (reconcile.Result, error) {
		calls = append(calls, scoped.eventAccountID)
		binding, err := appBindingForEvent(ctx, scoped.Bindings, "octo/api", "main", 42)
		if err != nil || binding.AccountID != scoped.eventAccountID {
			t.Fatalf("cross-account target: %+v %v", binding, err)
		}
		if _, err := appBindingForEvent(ctx, scoped.Bindings, "octo/api", "main", 99); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("wrong installation accessible: %v", err)
		}
		if scoped.eventAccountID == "a" {
			return reconcile.Result{}, failure
		}
		return reconcile.Result{BuildIDs: []string{"b-build"}}, nil
	})
	if !errors.Is(err, failure) || !slices.Equal(calls, []string{"a", "b"}) || !slices.Equal(result.BuildIDs, []string{"b-build"}) {
		t.Fatalf("dispatch = %+v %v calls=%v", result, err, calls)
	}
	if service.eventAccountID != "" {
		t.Fatal("shared service was mutated")
	}
}

func TestHandlePushRequestDeploysSameRepositoryToBothAccounts(t *testing.T) {
	ctx := context.Background()
	rig := newRig(t, func(fs.FS) (reposcan.Result, error) { return happyScan(), nil })
	rig.seedProject(t, "octo/api", "main")
	other, err := rig.mem.CreateAccount(ctx, "other@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	_, err = rig.mem.CreateProject(ctx, state.Project{AccountID: other.ID, Slug: "other-project", InstallID: rig.install, RepoFullName: "octo/api", ProductionBranch: "main", ScanSource: state.ProjectScanSourceSingle})
	if err != nil {
		t.Fatal(err)
	}
	service := newServiceForRig(t, rig)
	service.Bindings = sharedInstallationBindings{[]state.GitHubBinding{
		{BindingID: "first", AccountID: rig.acct, InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main"},
		{BindingID: "second", AccountID: other.ID, InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main"},
	}}
	service.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{
		rig.acct: {AccountID: rig.acct, InstallationID: 42}, other.ID: {AccountID: other.ID, InstallationID: 42},
	}}
	payload := []byte(`{"ref":"refs/heads/main","after":"cafebabe","repository":{"full_name":"octo/api","name":"api"},"installation":{"id":42}}`)
	result, err := service.HandlePushRequest(ctx, payload)
	if err != nil || len(result.Added) != 2 {
		t.Fatalf("shared repository deploy: added=%d error=%v", len(result.Added), err)
	}
	owners := map[string]bool{}
	for _, app := range result.Added {
		owners[app.AccountID] = true
	}
	if !owners[rig.acct] || !owners[other.ID] {
		t.Fatalf("wrong deploy owners: %v", owners)
	}
	// A redelivery must not create another app in either account.
	retry, err := service.HandlePushRequest(ctx, payload)
	if err != nil || len(retry.Added) != 0 {
		t.Fatalf("redelivery: added=%d error=%v", len(retry.Added), err)
	}
}
