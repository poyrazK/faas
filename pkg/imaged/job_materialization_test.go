package imaged

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type jobMaterializationPuller struct {
	digest   string
	manifest oci.Manifest
	config   oci.ImageConfig
	blob     []byte
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
	if want := sched.JobLayerKey(job.ID); got.ImageStorageKey != want {
		t.Fatalf("storage key = %q, want %q", got.ImageStorageKey, want)
	}
	rc, err := h.storage.Get(ctx, got.ImageStorageKey)
	if err != nil {
		t.Fatalf("Get published job artifact: %v", err)
	}
	defer rc.Close()
	if body, _ := io.ReadAll(rc); string(body) != "fake ext4 full-rootfs" {
		t.Fatalf("published artifact = %q, want fake builder output", body)
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

var _ oci.ManifestPuller = (*jobMaterializationPuller)(nil)
