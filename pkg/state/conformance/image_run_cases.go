package conformance

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1161
// testImageRunJobs pins the runner-job contract: hidden from listings and the
// Job quota, cached per name, re-queued on demand, image config recorded under
// the materialization claim, and evicted least-recently-used only when idle.
func testImageRunJobs(t *testing.T, fx *Fixture) {
	runners, ok := fx.Store.(state.ImageRunJobStore)
	if !ok {
		t.Fatalf("%T does not implement state.ImageRunJobStore", fx.Store)
	}
	const ref = "ghcr.io/onebox-faas/conformance:latest"
	countBefore, err := fx.Store.JobCountByAccount(fx.Ctx, fx.Account.ID)
	if err != nil {
		t.Fatalf("JobCountByAccount: %v", err)
	}
	nameA := "ir-" + uuid.NewString()[:8] + "aaaaaaaa"
	ensure := func(name string, pullAlways bool, maxCached int) (state.ImageRunEnsureResult, error) {
		return runners.JobEnsureImageRun(fx.Ctx, state.ImageRunEnsure{
			AccountID: fx.Account.ID, Name: name, ImageRef: ref,
			RAMMB: 128, TaskTimeoutSec: 60, PullAlways: pullAlways, MaxCached: maxCached,
		})
	}
	created, err := ensure(nameA, false, 2)
	if err != nil {
		t.Fatalf("ensure new runner: %v", err)
	}
	a := created.Job
	if !created.Created || a.Kind != state.JobKindImageRun || a.ImageMaterializationStatus != "pending" ||
		len(a.Command) != 0 || a.MaxParallelism != 1 || a.RetryMax != 0 || a.ImageConfigRecorded {
		t.Fatalf("created runner = %+v (created=%v)", a, created.Created)
	}
	assertImageRunHidden(t, fx, a.ID, countBefore)

	publishImageRunConfig(t, fx, a, []string{"/entry"}, nil)
	got, err := fx.Store.JobGetByID(fx.Ctx, a.ID)
	if err != nil {
		t.Fatalf("JobGetByID: %v", err)
	}
	if got.ImageMaterializationStatus != "ready" || !got.ImageConfigRecorded ||
		!reflect.DeepEqual(got.ImageEntrypoint, []string{"/entry"}) || len(got.ImageCmd) != 0 {
		t.Fatalf("published runner = %+v", got)
	}
	again, err := ensure(nameA, false, 2)
	if err != nil || again.Created || again.Requeued || again.Job.ImageMaterializationStatus != "ready" {
		t.Fatalf("cached ensure = %+v, %v", again, err)
	}
	pulled, err := ensure(nameA, true, 2)
	if err != nil || !pulled.Requeued || pulled.Job.ImageMaterializationStatus != "pending" || pulled.Job.ImageConfigRecorded {
		t.Fatalf("pull-always ensure = %+v, %v", pulled, err)
	}

	// At a cap of one, a new image evicts the idle runner A.
	nameB := "ir-" + uuid.NewString()[:8] + "bbbbbbbb"
	b, err := ensure(nameB, false, 1)
	if err != nil || !b.Created || len(b.Evicted) != 1 || b.Evicted[0].ID != a.ID {
		t.Fatalf("evicting ensure = %+v, %v", b, err)
	}
	if _, err := fx.Store.JobGetByID(fx.Ctx, a.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("evicted runner still visible: %v", err)
	}
	// A queued task pins B, so a third image finds the cache full.
	args := []string{"/bin/true"}
	parallelism := 1
	if _, _, err := fx.Store.JobRunCreate(fx.Ctx, b.Job.ID, fx.Account.ID, "manual", &parallelism,
		nil, nil, nil, 1, state.JobRunOptions{CommandArgs: &args}); err != nil {
		t.Fatalf("JobRunCreate on runner: %v", err)
	}
	var full *state.ImageRunCacheFullError
	if _, err := ensure("ir-"+uuid.NewString()[:8]+"cccccccc", false, 1); !errors.As(err, &full) || full.Limit != 1 || full.Observed != 1 {
		t.Fatalf("full cache ensure err = %v", err)
	}
	// A customer job holding the name is never adopted.
	taken := "ir-" + uuid.NewString()[:8] + "dddddddd"
	if _, err := fx.Store.JobCreate(fx.Ctx, fx.Account.ID, taken, "batch", ref, []string{"/bin/true"}, 128, 60, 1, 0, nil); err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	if _, err := ensure(taken, false, 5); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ensure over customer job err = %v, want ErrConflict", err)
	}
}

func assertImageRunHidden(t *testing.T, fx *Fixture, id string, countBefore int) {
	t.Helper()
	count, err := fx.Store.JobCountByAccount(fx.Ctx, fx.Account.ID)
	if err != nil || count != countBefore {
		t.Fatalf("JobCountByAccount = %d, %v; want %d", count, err, countBefore)
	}
	jobs, err := fx.Store.JobListByAccount(fx.Ctx, fx.Account.ID, 200, 0)
	if err != nil {
		t.Fatalf("JobListByAccount: %v", err)
	}
	for _, job := range jobs {
		if job.ID == id {
			t.Fatalf("runner job %s listed", id)
		}
	}
}

// publishImageRunConfig plays imaged: claim, record the image config under
// the claim, then publish the artifact.
func publishImageRunConfig(t *testing.T, fx *Fixture, job state.Job, entrypoint, cmd []string) {
	t.Helper()
	claimer, ok := fx.Store.(state.JobImageMaterializationClaimer)
	if !ok {
		t.Fatalf("%T does not implement JobImageMaterializationClaimer", fx.Store)
	}
	recorder, ok := fx.Store.(state.JobImageConfigRecorder)
	if !ok {
		t.Fatalf("%T does not implement JobImageConfigRecorder", fx.Store)
	}
	publisher, ok := fx.Store.(state.JobImageMaterializationPublisher)
	if !ok {
		t.Fatalf("%T does not implement JobImageMaterializationPublisher", fx.Store)
	}
	claimed, err := claimer.JobClaimImageMaterialization(fx.Ctx, job.ID, "conformance", time.Minute)
	if err != nil {
		t.Fatalf("JobClaimImageMaterialization: %v", err)
	}
	attempt := claimed.ImageMaterializationAttempts
	if err := recorder.JobRecordImageConfig(fx.Ctx, job.ID, job.ImageRef, "intruder", attempt, entrypoint, cmd); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("record config with a foreign owner err = %v, want ErrConflict", err)
	}
	if err := recorder.JobRecordImageConfig(fx.Ctx, job.ID, job.ImageRef, "conformance", attempt, entrypoint, cmd); err != nil {
		t.Fatalf("JobRecordImageConfig: %v", err)
	}
	if _, err := publisher.JobPublishImageMaterialization(fx.Ctx, job.ID, job.ImageRef, "conformance", attempt,
		"sha256:"+uuid.NewString(), "jobs/"+job.ID+"/conformance.ext4"); err != nil {
		t.Fatalf("JobPublishImageMaterialization: %v", err)
	}
}
