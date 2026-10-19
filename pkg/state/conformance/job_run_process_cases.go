package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1790
// testJobRunProcess pins an image run's -u/-w snapshot: it is stored with the
// run, read back, absent on an ordinary run, rejected when invalid by both
// stores, and kept by a replay of the run.
func testJobRunProcess(t *testing.T, fx *Fixture) {
	name := "process-" + uuid.NewString()[:8]
	job, err := fx.Store.JobCreate(fx.Ctx, fx.Account.ID, name, "batch",
		"ghcr.io/onebox-faas/conformance:latest", []string{"/bin/sh"}, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatalf("JobCreate: %v", err)
	}
	materializer, ok := fx.Store.(interface {
		JobSetImageMaterialization(context.Context, string, string, string, string, string, string) (state.Job, error)
	})
	if !ok {
		t.Fatal("store does not implement job image materialization")
	}
	if _, err := materializer.JobSetImageMaterialization(fx.Ctx, job.ID, job.ImageRef, "ready",
		"sha256:"+strings.Repeat("c", 64), "jobs/conformance-process.ext4", ""); err != nil {
		t.Fatalf("JobSetImageMaterialization: %v", err)
	}
	one := 1
	create := func(opts state.JobRunOptions) (state.JobRun, error) {
		run, _, err := fx.Store.JobRunCreate(fx.Ctx, job.ID, fx.Account.ID, "manual", &one, nil, nil, nil, 1, opts)
		return run, err
	}

	for label, opts := range map[string]state.JobRunOptions{
		"relative workdir": {ProcessWorkdir: "srv"},
		"workdir too long": {ProcessWorkdir: "/" + strings.Repeat("d", 4096)},
		"user too long":    {ProcessUser: strings.Repeat("u", 257)},
	} {
		if _, err := create(opts); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("JobRunCreate(%s) = %v, want ErrInvalidArgument", label, err)
		}
	}

	run, err := create(state.JobRunOptions{ProcessUser: "app:staff", ProcessWorkdir: "/srv/data"})
	if err != nil {
		t.Fatalf("JobRunCreate: %v", err)
	}
	if run.ProcessUser != "app:staff" || run.ProcessWorkdir != "/srv/data" {
		t.Fatalf("created run process = %q %q", run.ProcessUser, run.ProcessWorkdir)
	}
	got, err := fx.Store.JobRunGetByID(fx.Ctx, run.ID)
	if err != nil || got.ProcessUser != "app:staff" || got.ProcessWorkdir != "/srv/data" {
		t.Fatalf("JobRunGetByID process = %q %q, %v", got.ProcessUser, got.ProcessWorkdir, err)
	}
	plain, err := create(state.JobRunOptions{})
	if err != nil {
		t.Fatalf("plain JobRunCreate: %v", err)
	}
	if got, err := fx.Store.JobRunGetByID(fx.Ctx, plain.ID); err != nil || got.ProcessUser != "" || got.ProcessWorkdir != "" {
		t.Fatalf("plain run process = %q %q, %v", got.ProcessUser, got.ProcessWorkdir, err)
	}

	if err := fx.Store.JobTaskMarkTerminal(fx.Ctx, run.ID, 0, "failed", 1, "failed", "exit 1", time.Now()); err != nil {
		t.Fatalf("JobTaskMarkTerminal: %v", err)
	}
	if recomputed, err := fx.Store.JobRunRecompute(fx.Ctx, run.ID); err != nil || recomputed.FinishedAt == nil {
		t.Fatalf("JobRunRecompute = %+v, %v", recomputed, err)
	}
	replay, _, err := fx.Store.JobRunReplayFailed(fx.Ctx, run.ID, fx.Account.ID)
	if err != nil {
		t.Fatalf("JobRunReplayFailed: %v", err)
	}
	if replay.ProcessUser != "app:staff" || replay.ProcessWorkdir != "/srv/data" {
		t.Fatalf("replay process = %q %q; a replay keeps the run's overrides", replay.ProcessUser, replay.ProcessWorkdir)
	}
}
