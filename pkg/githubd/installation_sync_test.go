package githubd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type syncStoreFake struct {
	installs   []state.GitHubInstall
	bindings   map[int64][]state.GitHubBinding
	listErr    error
	bindingErr map[int64]error
	removeErr  map[int64]error
	recordErr  map[int64]error
	removed    map[int64][]string
	records    map[int64]syncRecord
}

type syncRecord struct {
	err      string
	remote   int
	detached int
}

func (f *syncStoreFake) ListGitHubInstallations(context.Context) ([]state.GitHubInstall, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]state.GitHubInstall(nil), f.installs...), nil
}

func (f *syncStoreFake) ListGitHubInstallBindingsForInstallation(_ context.Context, installationID int64) ([]state.GitHubBinding, error) {
	if err := f.bindingErr[installationID]; err != nil {
		return nil, err
	}
	return append([]state.GitHubBinding(nil), f.bindings[installationID]...), nil
}

func (f *syncStoreFake) RemoveGitHubRepositories(_ context.Context, installationID int64, names []string) error {
	if err := f.removeErr[installationID]; err != nil {
		return err
	}
	if f.removed == nil {
		f.removed = make(map[int64][]string)
	}
	f.removed[installationID] = append([]string(nil), names...)
	return nil
}

func (f *syncStoreFake) RecordGitHubInstallationSync(_ context.Context, installationID int64, _ time.Time, errText string, remote, detached int) error {
	if err := f.recordErr[installationID]; err != nil {
		return err
	}
	if f.records == nil {
		f.records = make(map[int64]syncRecord)
	}
	f.records[installationID] = syncRecord{err: errText, remote: remote, detached: detached}
	return nil
}

type syncClientFake struct {
	byInstall map[int64][]githubdgrpc.Repo
	errs      map[int64]error
}

func (f *syncClientFake) ListInstallableReposContext(_ context.Context, _ string, installationID int64) ([]githubdgrpc.Repo, error) {
	if err := f.errs[installationID]; err != nil {
		return nil, err
	}
	return append([]githubdgrpc.Repo(nil), f.byInstall[installationID]...), nil
}

func TestInstallationSyncerDetachesOnlyMissingRepositories(t *testing.T) {
	store := &syncStoreFake{
		installs: []state.GitHubInstall{{AccountID: "acct-1", InstallationID: 42}},
		bindings: map[int64][]state.GitHubBinding{42: {
			{RepoFullName: "Acme/Service"},
			{RepoFullName: "acme/Removed"},
		}},
		bindingErr: make(map[int64]error), removeErr: make(map[int64]error), recordErr: make(map[int64]error),
	}
	client := &syncClientFake{
		byInstall: map[int64][]githubdgrpc.Repo{42: {
			{FullName: "acme/service"},
			{FullName: "acme/new-repository"},
		}},
		errs: make(map[int64]error),
	}

	summary, err := NewInstallationSyncer(store, client).SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("SyncOnce() error = %v", err)
	}
	if summary.DetachedBindings != 1 || summary.RemoteRepositories != 2 || summary.ExistingBindings != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	got := store.removed[42]
	if len(got) != 1 || got[0] != "acme/Removed" {
		t.Fatalf("removed repositories = %#v", got)
	}
	if got := store.records[42]; got.err != "" || got.remote != 2 || got.detached != 1 {
		t.Fatalf("sync record = %+v", got)
	}
}

func TestInstallationSyncerDoesNotDetachOnGitHubFailure(t *testing.T) {
	remoteErr := errors.New("GitHub unavailable")
	store := &syncStoreFake{
		installs:   []state.GitHubInstall{{AccountID: "acct-1", InstallationID: 42}},
		bindings:   map[int64][]state.GitHubBinding{42: {{RepoFullName: "acme/service"}}},
		bindingErr: make(map[int64]error), removeErr: make(map[int64]error), recordErr: make(map[int64]error),
	}
	client := &syncClientFake{byInstall: make(map[int64][]githubdgrpc.Repo), errs: map[int64]error{42: remoteErr}}

	summary, err := NewInstallationSyncer(store, client).SyncOnce(context.Background())
	if err == nil || !errors.Is(err, remoteErr) {
		t.Fatalf("SyncOnce() error = %v, want wrapped remote error", err)
	}
	if summary.Failures != 1 || len(store.removed) != 0 {
		t.Fatalf("unexpected failure summary/removals: %+v %#v", summary, store.removed)
	}
	if got := store.records[42]; got.err != remoteErr.Error() || got.detached != 0 {
		t.Fatalf("sync record = %+v", got)
	}
}

func TestInstallationSyncerContinuesAfterOneInstallationFails(t *testing.T) {
	store := &syncStoreFake{
		installs: []state.GitHubInstall{
			{AccountID: "acct-1", InstallationID: 41},
			{AccountID: "acct-2", InstallationID: 42},
		},
		bindings: map[int64][]state.GitHubBinding{
			41: {{RepoFullName: "acme/old"}},
			42: {{RepoFullName: "acme/keep"}},
		},
		bindingErr: make(map[int64]error), removeErr: make(map[int64]error), recordErr: make(map[int64]error),
	}
	client := &syncClientFake{
		byInstall: map[int64][]githubdgrpc.Repo{42: {{FullName: "acme/keep"}}},
		errs:      map[int64]error{41: errors.New("expired token")},
	}

	summary, err := NewInstallationSyncer(store, client).SyncOnce(context.Background())
	if err == nil || summary.Installations != 2 || summary.Failures != 1 {
		t.Fatalf("unexpected summary/error: %+v %v", summary, err)
	}
	if len(store.records) != 2 {
		t.Fatalf("records = %#v, want both installations recorded", store.records)
	}
}

func TestInstallationSyncerRecordsDetachFailure(t *testing.T) {
	removeErr := errors.New("database unavailable")
	store := &syncStoreFake{
		installs:   []state.GitHubInstall{{AccountID: "acct-1", InstallationID: 42}},
		bindings:   map[int64][]state.GitHubBinding{42: {{RepoFullName: "acme/removed"}}},
		bindingErr: make(map[int64]error), removeErr: map[int64]error{42: removeErr}, recordErr: make(map[int64]error),
	}
	client := &syncClientFake{
		byInstall: map[int64][]githubdgrpc.Repo{42: nil},
		errs:      make(map[int64]error),
	}

	summary, err := NewInstallationSyncer(store, client).SyncOnce(context.Background())
	if err == nil || !errors.Is(err, removeErr) || summary.DetachedBindings != 0 {
		t.Fatalf("unexpected summary/error: %+v %v", summary, err)
	}
	if got := store.records[42]; got.err == "" || got.detached != 0 {
		t.Fatalf("sync record = %+v, want detach failure", got)
	}
}
