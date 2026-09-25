package imaged

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

type jobMaterializationPuller struct {
	digest   string
	manifest oci.Manifest
	config   oci.ImageConfig
	blob     []byte
	authSeen []oci.BasicAuth
}

type gatedJobMaterializationBuilder struct {
	*fakeBuilder
	keyReady chan<- string
	release  <-chan struct{}
	body     string
}

func (b *gatedJobMaterializationBuilder) BuildFullRootfs(ctx context.Context, in rootfs.BuildFullRootfsInput) (rootfs.BuildResult, error) {
	if err := in.Storage.Put(ctx, in.StorageKey, strings.NewReader(b.body)); err != nil {
		return rootfs.BuildResult{}, err
	}
	b.keyReady <- in.StorageKey
	select {
	case <-b.release:
		return rootfs.BuildResult{ImageKey: in.StorageKey}, nil
	case <-ctx.Done():
		return rootfs.BuildResult{}, ctx.Err()
	}
}

func (p *jobMaterializationPuller) PullDigest(context.Context, string) (string, error) {
	return p.digest, nil
}
func (p *jobMaterializationPuller) PullImageConfig(context.Context, string) (oci.ImageConfig, error) {
	return p.config, nil
}
func (p *jobMaterializationPuller) PullLayers(context.Context, string) (oci.PullLayersResult, error) {
	return oci.PullLayersResult{}, nil
}
func (p *jobMaterializationPuller) PullManifest(context.Context, string) (oci.Manifest, error) {
	return p.manifest, nil
}
func (p *jobMaterializationPuller) PullBlob(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(p.blob)), nil
}

func (p *jobMaterializationPuller) rememberAuth(auth *oci.BasicAuth) {
	if auth != nil {
		p.authSeen = append(p.authSeen, *auth)
	}
}
func (p *jobMaterializationPuller) PullDigestWithAuth(ctx context.Context, ref string, auth *oci.BasicAuth) (string, error) {
	p.rememberAuth(auth)
	return p.PullDigest(ctx, ref)
}
func (p *jobMaterializationPuller) PullImageConfigWithAuth(ctx context.Context, ref string, auth *oci.BasicAuth) (oci.ImageConfig, error) {
	p.rememberAuth(auth)
	return p.PullImageConfig(ctx, ref)
}
func (p *jobMaterializationPuller) PullLayersWithAuth(ctx context.Context, ref string, auth *oci.BasicAuth) (oci.PullLayersResult, error) {
	p.rememberAuth(auth)
	return p.PullLayers(ctx, ref)
}
func (p *jobMaterializationPuller) PullManifestWithAuth(ctx context.Context, ref string, auth *oci.BasicAuth) (oci.Manifest, error) {
	p.rememberAuth(auth)
	return p.PullManifest(ctx, ref)
}
func (p *jobMaterializationPuller) PullBlobWithAuth(ctx context.Context, repo, digest string, auth *oci.BasicAuth) (io.ReadCloser, error) {
	p.rememberAuth(auth)
	return p.PullBlob(ctx, repo, digest)
}

func TestMaterializeJobPublishesResolvedArtifact(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "job-materialize@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := store.JobCreate(ctx, acct.ID, "materialize", "batch",
		"registry.example/worker:latest", []string{"/bin/worker"},
		256, 60, 2, 1, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	puller := &jobMaterializationPuller{
		digest:   digest,
		manifest: oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:" + strings.Repeat("b", 64)}}},
		config:   oci.ImageConfig{},
		blob:     []byte("gzip-tar-placeholder"),
	}
	appsRoot := t.TempDir()
	h := New(store, &fakeNotifier{}, puller, &fakeBuilder{bytesOut: 123}, "./guest-init", appsRoot, silentLogger()).
		WithStorage(mustLocalStorage(t, appsRoot))

	if err := h.HandleNotification(ctx, db.Notification{
		Channel: db.NotifyJobChanged,
		Payload: `{"kind":"created","job_id":"` + job.ID + `"}`,
	}); err != nil {
		t.Fatalf("HandleNotification(job_changed): %v", err)
	}
	got, err := store.JobGetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("JobGetByID: %v", err)
	}
	if got.ImageMaterializationStatus != "ready" || got.ImageResolvedDigest != digest {
		t.Fatalf("materialization state = %+v, want ready + digest", got)
	}
	if got.ImageStorageKey == sched.JobLayerKey(job.ID) || !strings.HasPrefix(got.ImageStorageKey, "jobs/"+job.ID+"__") {
		t.Fatalf("storage key = %q, want an immutable per-attempt key", got.ImageStorageKey)
	}
	rc, err := h.storage.Get(ctx, got.ImageStorageKey)
	if err != nil {
		t.Fatalf("Get published job artifact: %v", err)
	}
	defer rc.Close()
	if body, _ := io.ReadAll(rc); string(body) != "fake ext4 full-rootfs" {
		t.Fatalf("published artifact = %q, want fake builder output", body)
	}
	deleted, hasLive, err := store.JobSoftDelete(ctx, job.ID)
	if err != nil || !deleted || hasLive {
		t.Fatalf("JobSoftDelete = deleted:%v live:%v err:%v, want deleted without live tasks", deleted, hasLive, err)
	}
	if err := h.HandleNotification(ctx, db.Notification{
		Channel: db.NotifyJobChanged,
		Payload: `{"kind":"deleted","job_id":"` + job.ID + `"}`,
	}); err != nil {
		t.Fatalf("HandleNotification(deleted job_changed): %v", err)
	}
	if _, err := h.storage.Get(ctx, got.ImageStorageKey); !storage.IsNotFound(err) {
		t.Fatalf("deleted job artifact error = %v, want storage not found", err)
	}
}

func TestMaterializeJobStaleAttemptCannotReplaceOrDeleteWinner(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "job-materialize-race@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := store.JobCreate(ctx, acct.ID, "materialize-race", "batch",
		"registry.example/worker:v1", []string{"/bin/worker"}, 256, 60, 2, 1, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	oldKeyReady := make(chan string, 1)
	releaseOld := make(chan struct{})
	oldPuller := &jobMaterializationPuller{
		digest:   "sha256:" + strings.Repeat("1", 64),
		manifest: oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:" + strings.Repeat("a", 64)}}},
		config:   oci.ImageConfig{},
		blob:     []byte("old-layer"),
	}
	newPuller := &jobMaterializationPuller{
		digest:   "sha256:" + strings.Repeat("2", 64),
		manifest: oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:" + strings.Repeat("b", 64)}}},
		config:   oci.ImageConfig{},
		blob:     []byte("new-layer"),
	}
	root := t.TempDir()
	storageBackend := mustLocalStorage(t, root)
	oldHandler := New(store, &fakeNotifier{}, oldPuller, &gatedJobMaterializationBuilder{
		fakeBuilder: &fakeBuilder{}, keyReady: oldKeyReady, release: releaseOld, body: "stale artifact",
	}, "./guest-init", root, silentLogger()).WithStorage(storageBackend)
	newHandler := New(store, &fakeNotifier{}, newPuller, &fakeBuilder{bytesOut: 123}, "./guest-init", root, silentLogger()).WithStorage(storageBackend)

	oldDone := make(chan error, 1)
	go func() { oldDone <- oldHandler.MaterializeJob(ctx, job.ID) }()
	oldKey := <-oldKeyReady
	newRef := "registry.example/worker:v2"
	if _, err := store.JobUpdate(ctx, job.ID, nil, &newRef, nil, nil, nil, nil, nil, nil); err != nil {
		close(releaseOld)
		t.Fatalf("JobUpdate image ref: %v", err)
	}
	if err := newHandler.MaterializeJob(ctx, job.ID); err != nil {
		close(releaseOld)
		t.Fatalf("new MaterializeJob: %v", err)
	}
	close(releaseOld)
	if err := <-oldDone; err == nil {
		t.Fatal("stale MaterializeJob succeeded, want its claim to be fenced")
	}

	winner, err := store.JobGetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("JobGetByID: %v", err)
	}
	if winner.ImageMaterializationStatus != "ready" || winner.ImageResolvedDigest != newPuller.digest || winner.ImageStorageKey == oldKey {
		t.Fatalf("winning image state = %+v, want the new attempt", winner)
	}
	if _, err := storageBackend.Get(ctx, oldKey); !storage.IsNotFound(err) {
		t.Fatalf("stale artifact lookup error = %v, want not found", err)
	}
	artifact, err := storageBackend.Get(ctx, winner.ImageStorageKey)
	if err != nil {
		t.Fatalf("read winning artifact: %v", err)
	}
	defer artifact.Close()
	body, err := io.ReadAll(artifact)
	if err != nil || string(body) != "fake ext4 full-rootfs" {
		t.Fatalf("winning artifact = %q, %v; want new build output", body, err)
	}
}

func TestReconcileDeletedJobArtifactsWithoutNotification(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "job-artifact-reconcile@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := store.JobCreate(ctx, acct.ID, "orphaned-artifact", "batch",
		"registry.example/worker:latest", []string{"/bin/worker"},
		256, 60, 2, 1, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	root := t.TempDir()
	h := New(store, &fakeNotifier{}, nil, nil, "", root, silentLogger()).
		WithStorage(mustLocalStorage(t, root))
	key := sched.JobLayerKey(job.ID)
	if err := h.storage.Put(ctx, key, strings.NewReader("orphaned job image")); err != nil {
		t.Fatalf("Put job artifact: %v", err)
	}
	attemptKey := sched.JobLayerAttemptKey(job.ID, "01234567-89ab-cdef-0123-456789abcdef")
	if err := h.storage.Put(ctx, attemptKey, strings.NewReader("orphaned attempt image")); err != nil {
		t.Fatalf("Put attempt artifact: %v", err)
	}
	deleted, hasLive, err := store.JobSoftDelete(ctx, job.ID)
	if err != nil || !deleted || hasLive {
		t.Fatalf("JobSoftDelete = deleted:%v live:%v err:%v, want deleted without live tasks", deleted, hasLive, err)
	}
	if err := h.ReconcileDeletedJobArtifacts(ctx); err != nil {
		t.Fatalf("ReconcileDeletedJobArtifacts: %v", err)
	}
	if _, err := h.storage.Get(ctx, key); !storage.IsNotFound(err) {
		t.Fatalf("reconciled job artifact error = %v, want storage not found", err)
	}
	if _, err := h.storage.Get(ctx, attemptKey); !storage.IsNotFound(err) {
		t.Fatalf("reconciled attempt artifact error = %v, want storage not found", err)
	}
}

func TestMaterializeJobUsesJobRegistryCredential(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "job-registry-auth@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	job, err := store.JobCreate(ctx, acct.ID, "private-materialize", "batch",
		"registry.example/worker:latest", []string{"/bin/worker"},
		256, 60, 2, 1, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("Generate identity: %v", err)
	}
	ciphertext, err := secretbox.SealBytes(identity.Recipient(), "registry_creds", []byte("job-secret"), 4096)
	if err != nil {
		t.Fatalf("SealBytes: %v", err)
	}
	if err := store.UpsertJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example", "robot", ciphertext); err != nil {
		t.Fatalf("UpsertJobRegistryCredential: %v", err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	puller := &jobMaterializationPuller{
		digest:   digest,
		manifest: oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:" + strings.Repeat("d", 64)}}},
		config:   oci.ImageConfig{},
		blob:     []byte("gzip-tar-placeholder"),
	}
	appsRoot := t.TempDir()
	h := New(store, &fakeNotifier{}, puller, &fakeBuilder{bytesOut: 123}, "./guest-init", appsRoot, silentLogger()).
		WithSecretboxIdentity(identity).
		WithStorage(mustLocalStorage(t, appsRoot))
	if err := h.HandleNotification(ctx, db.Notification{
		Channel: db.NotifyJobChanged,
		Payload: `{"kind":"created","job_id":"` + job.ID + `"}`,
	}); err != nil {
		t.Fatalf("HandleNotification(job_changed): %v", err)
	}
	if len(puller.authSeen) != 4 {
		t.Fatalf("auth calls = %d, want digest/manifest/config/blob", len(puller.authSeen))
	}
	for i, got := range puller.authSeen {
		if got.Username != "robot" || got.Password != "job-secret" {
			t.Fatalf("auth call %d = %+v, want robot/job-secret", i, got)
		}
	}
	cred, err := store.GetJobRegistryCredential(ctx, acct.ID, job.ID, "registry.example")
	if err != nil {
		t.Fatalf("GetJobRegistryCredential: %v", err)
	}
	if cred.LastUsedAt == nil {
		t.Fatal("LastUsedAt = nil after successful authenticated materialization")
	}
}

func TestJobMaterializationRetryDelayIsBounded(t *testing.T) {
	if got := jobMaterializationRetryDelay(1); got != 5*time.Second {
		t.Fatalf("attempt 1 delay = %s, want 5s", got)
	}
	if got := jobMaterializationRetryDelay(2); got != 10*time.Second {
		t.Fatalf("attempt 2 delay = %s, want 10s", got)
	}
	if got := jobMaterializationRetryDelay(20); got != jobMaterializationRetryMax {
		t.Fatalf("large attempt delay = %s, want cap %s", got, jobMaterializationRetryMax)
	}
}

type failingPendingJobClaimStore struct {
	*state.MemStore
	err error
}

func (s *failingPendingJobClaimStore) JobClaimPendingImageMaterialization(context.Context, int, string, time.Duration) ([]state.Job, error) {
	return nil, s.err
}

func TestPendingJobMaterializationClaimFailureIsObservable(t *testing.T) {
	store := &failingPendingJobClaimStore{MemStore: state.NewMemStore(), err: errors.New("claim failed")}
	ops := wire.NewOpsMetrics("imaged_test")
	h := New(store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger()).WithOpsMetrics(ops)
	if err := h.MaterializePendingJobs(context.Background()); !errors.Is(err, store.err) {
		t.Fatalf("MaterializePendingJobs error = %v, want %v", err, store.err)
	}

	recorder := httptest.NewRecorder()
	ops.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if body := recorder.Body.String(); !strings.Contains(body, `imaged_test_ops_total{code="err",op="job_materialization_claim"} 1`) {
		t.Fatalf("metrics missing pending job claim failure:\n%s", body)
	}
}

var _ oci.ManifestPuller = (*jobMaterializationPuller)(nil)
var _ oci.AuthPuller = (*jobMaterializationPuller)(nil)
var _ oci.AuthManifestPuller = (*jobMaterializationPuller)(nil)
