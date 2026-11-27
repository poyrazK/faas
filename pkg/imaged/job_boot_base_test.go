package imaged

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMaterializeJobCannotPublishBeforeBootBase(t *testing.T) {
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", "")
	t.Setenv("FAAS_DEPLOY_BASE_REF_MINIMAL", "registry.example/base:unpinned")
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "boot-base@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, account.ID, "boot-base", "batch",
		"registry.example/job:latest", []string{"/worker"}, 256, 60, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	builder := &fakeBuilder{bytesOut: 123}
	puller := &jobMaterializationPuller{digest: "sha256:" + strings.Repeat("a", 64), manifest: oci.Manifest{}}
	root := t.TempDir()
	h := New(store, &fakeNotifier{}, puller, builder, "./guest-init", root, silentLogger()).
		WithStorage(mustLocalStorage(t, root)).WithRuntimeBaseStaging()
	if err := h.MaterializeJob(ctx, job.ID); err == nil || !strings.Contains(err.Error(), "ensure job boot base") {
		t.Fatalf("missing boot base did not block materialization: %v", err)
	}
	current, err := store.JobGetByID(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ImageMaterializationStatus == "ready" || current.ImageStorageKey != "" || len(builder.fullRootfsCalls) != 0 {
		t.Fatalf("job published before its boot base: %+v", current)
	}
}
