package githubd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"
)

type lifecycleRecorder struct {
	revoked []int64
	removed []struct {
		id    int64
		repos []string
	}
	renamed []struct {
		id               int64
		oldName, newName string
	}
}

func (r *lifecycleRecorder) RevokeGitHubInstallation(_ context.Context, id int64) error {
	r.revoked = append(r.revoked, id)
	return nil
}

func (r *lifecycleRecorder) RemoveGitHubRepositories(_ context.Context, id int64, repos []string) error {
	r.removed = append(r.removed, struct {
		id    int64
		repos []string
	}{id, append([]string(nil), repos...)})
	return nil
}

func (r *lifecycleRecorder) RenameGitHubRepository(_ context.Context, id int64, oldName, newName string) error {
	r.renamed = append(r.renamed, struct {
		id               int64
		oldName, newName string
	}{id, oldName, newName})
	return nil
}

func TestDecodeInstallationLifecycleEvents(t *testing.T) {
	installation, err := DecodeInstallation([]byte(`{"action":"deleted","installation":{"id":42}}`))
	if err != nil || installation.Installation.ID != 42 || installation.Action != "deleted" {
		t.Fatalf("DecodeInstallation = %#v, %v", installation, err)
	}
	repositories, err := DecodeInstallationRepositories([]byte(`{"action":"removed","installation":{"id":42},"repositories_removed":[{"full_name":"octo/api"}]}`))
	if err != nil || repositories.Installation.ID != 42 || len(repositories.RepositoriesRemoved) != 1 {
		t.Fatalf("DecodeInstallationRepositories = %#v, %v", repositories, err)
	}
	repository, err := DecodeRepository([]byte(`{"action":"renamed","installation":{"id":42},"repository":{"full_name":"octo/new-api","owner":{"login":"octo"}},"changes":{"repository":{"name":{"from":"api"}}}}`))
	if err != nil || repository.Repository.FullName != "octo/new-api" {
		t.Fatalf("DecodeRepository = %#v, %v", repository, err)
	}
}

func TestHandleInstallationLifecycleRevokesAndInvalidates(t *testing.T) {
	recorder := &lifecycleRecorder{}
	invalidated := int64(0)
	secretInvalidated := int64(0)
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Lifecycle = recorder
	svc.InvalidateInstallation = func(id int64) { invalidated = id }
	svc.InvalidateInstallationSecrets = func(id int64) { secretInvalidated = id }
	err := svc.HandleWebhookEvent(context.Background(), "installation", []byte(`{"action":"suspend","installation":{"id":77}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorder.revoked, []int64{77}) || invalidated != 77 || secretInvalidated != 77 {
		t.Fatalf("revoked=%v invalidated=%d secretInvalidated=%d", recorder.revoked, invalidated, secretInvalidated)
	}
}

func TestHandleInstallationRepositoriesDeduplicatesRemovedRepos(t *testing.T) {
	recorder := &lifecycleRecorder{}
	invalidated := int64(0)
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Lifecycle = recorder
	svc.InvalidateInstallationBindings = func(id int64) { invalidated = id }
	err := svc.HandleWebhookEvent(context.Background(), "installation_repositories", []byte(`{"action":"removed","installation":{"id":88},"repositories_removed":[{"full_name":"octo/api"},{"full_name":"octo/api"},{"full_name":""},{"full_name":"octo/web"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.removed) != 1 || recorder.removed[0].id != 88 || !reflect.DeepEqual(recorder.removed[0].repos, []string{"octo/api", "octo/web"}) {
		t.Fatalf("removed=%v", recorder.removed)
	}
	if invalidated != 88 {
		t.Fatalf("invalidated=%d, want 88", invalidated)
	}
}

func TestHandleRepositoryRenameReconstructsOldFullName(t *testing.T) {
	recorder := &lifecycleRecorder{}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Lifecycle = recorder
	err := svc.HandleWebhookEvent(context.Background(), "repository", []byte(`{"action":"renamed","installation":{"id":99},"repository":{"full_name":"octo/new-api","owner":{"login":"octo"}},"changes":{"repository":{"name":{"from":"api"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id               int64
		oldName, newName string
	}{{99, "octo/api", "octo/new-api"}}
	if !reflect.DeepEqual(recorder.renamed, want) {
		t.Fatalf("renamed=%v, want %v", recorder.renamed, want)
	}
}

func TestHandleRepositoryTransferUsesPreviousOwner(t *testing.T) {
	recorder := &lifecycleRecorder{}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Lifecycle = recorder
	err := svc.HandleWebhookEvent(context.Background(), "repository", []byte(`{"action":"transferred","installation":{"id":100},"repository":{"name":"api","full_name":"new-owner/api","owner":{"login":"new-owner"}},"changes":{"owner":{"from":{"login":"old-owner"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id               int64
		oldName, newName string
	}{{100, "old-owner/api", "new-owner/api"}}
	if !reflect.DeepEqual(recorder.renamed, want) {
		t.Fatalf("renamed=%v, want %v", recorder.renamed, want)
	}
}

func TestHandleInstallationLifecycleUnknownActionIsAcknowledged(t *testing.T) {
	recorder := &lifecycleRecorder{}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Lifecycle = recorder
	if err := svc.HandleWebhookEvent(context.Background(), "installation", []byte(`{"action":"created","installation":{"id":1}}`)); err != nil {
		t.Fatal(err)
	}
	if len(recorder.revoked) != 0 {
		t.Fatalf("created action revoked installation: %v", recorder.revoked)
	}
}

func TestHandleInstallationLifecyclePropagatesStoreError(t *testing.T) {
	want := errors.New("db unavailable")
	recorder := &lifecycleRecorderWithError{err: want}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Lifecycle = recorder
	err := svc.HandleWebhookEvent(context.Background(), "installation", []byte(`{"action":"deleted","installation":{"id":2}}`))
	if !errors.Is(err, want) {
		t.Fatalf("err=%v, want %v", err, want)
	}
}

func TestTokenCacheInvalidateCancelsInFlightRefreshResult(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	cache := NewTokenCache(fakeFetcher(func(context.Context, int64) (string, time.Time, error) {
		close(started)
		<-release
		return "ghs-revoked", time.Now().Add(time.Hour), nil
	}), time.Minute)
	result := make(chan error, 1)
	go func() {
		_, err := cache.Token(context.Background(), 42)
		result <- err
	}()
	<-started
	cache.Invalidate(42)
	close(release)
	if err := <-result; !errors.Is(err, ErrInstallationInvalidated) {
		t.Fatalf("Token error = %v, want ErrInstallationInvalidated", err)
	}
	if _, ok := cache.ExpiresAt(42); ok {
		t.Fatal("revoked installation repopulated token cache")
	}
}

type lifecycleRecorderWithError struct{ err error }

func (r *lifecycleRecorderWithError) RevokeGitHubInstallation(context.Context, int64) error {
	return r.err
}
func (r *lifecycleRecorderWithError) RemoveGitHubRepositories(context.Context, int64, []string) error {
	return r.err
}
func (r *lifecycleRecorderWithError) RenameGitHubRepository(context.Context, int64, string, string) error {
	return r.err
}
