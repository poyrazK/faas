package nodejoin

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreLeaseAndResume(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	spec := Spec{NodeName: "fsn-2", DatabaseNode: "fsn-2.faas", SSHHost: "203.0.113.20", ManifestHash: "sha256:" + "a", ReleaseGitSHA: "b"}
	if _, err := s.CreateOrResume(ctx, spec, false); err != nil {
		t.Fatalf("CreateOrResume: %v", err)
	}
	if _, err := s.AcquireLease(ctx, "fsn-2", "worker-a", time.Minute); err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	if _, err := s.AcquireLease(ctx, "fsn-2", "worker-b", time.Minute); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second AcquireLease = %v, want ErrLeaseHeld", err)
	}
	if err := s.UpdatePhase(ctx, "fsn-2", "worker-a", PhaseConverging, ""); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}
	if err := s.UpdatePhase(ctx, "fsn-2", "worker-a", PhasePrepared, ""); err != nil {
		t.Fatalf("UpdatePhase prepared: %v", err)
	}
	if prepared, err := s.Get(ctx, "fsn-2"); err != nil || prepared.Phase != PhasePrepared {
		t.Fatalf("prepared job = %#v, err=%v", prepared, err)
	}
	if err := s.MarkFailed(ctx, "fsn-2", "worker-a", errors.New("connection reset")); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	job, err := s.Get(ctx, "fsn-2")
	if err != nil || job.Phase != PhaseFailed || job.LastError != "connection reset" {
		t.Fatalf("failed job = %#v, err=%v", job, err)
	}
	if _, err := s.CreateOrResume(ctx, spec, false); !errors.Is(err, ErrResumeRequired) {
		t.Fatalf("same failed job without resume = %v, want ErrResumeRequired", err)
	}
	if _, err := s.CreateOrResume(ctx, spec, true); err != nil {
		t.Fatalf("resume: %v", err)
	}
	job, err = s.AcquireLease(ctx, "fsn-2", "worker-c", time.Minute)
	if err != nil {
		t.Fatalf("AcquireLease after resume: %v", err)
	}
	if job.Phase != PhasePlanned || job.Attempt != 2 {
		t.Fatalf("resumed job = %#v, want planned attempt 2", job)
	}
	if err := s.MarkRolledBack(ctx, "fsn-2", "worker-c", errors.New("operator rollback")); err != nil {
		t.Fatalf("MarkRolledBack: %v", err)
	}
	job, err = s.Get(ctx, "fsn-2")
	if err != nil || job.Phase != PhaseRolledBack {
		t.Fatalf("rolled-back job = %#v, err=%v", job, err)
	}
}

func TestMemStoreRejectsDifferentDesiredState(t *testing.T) {
	ctx := context.Background()
	s := NewMemStore()
	spec := Spec{NodeName: "fsn-3", DatabaseNode: "fsn-3.faas", SSHHost: "203.0.113.21", ManifestHash: "sha256:a", ReleaseGitSHA: "b"}
	if _, err := s.CreateOrResume(ctx, spec, false); err != nil {
		t.Fatal(err)
	}
	spec.SSHHost = "203.0.113.22"
	if _, err := s.CreateOrResume(ctx, spec, false); !errors.Is(err, ErrExisting) {
		t.Fatalf("different desired state = %v, want ErrExisting", err)
	}
}
