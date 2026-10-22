//go:build !no_pg

// adr: 590
package apidserver

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGCloneCheckpointCaptureRetirementObjectDependencies(t *testing.T) {
	for _, phase := range []string{"planned-creation", "unknown-creation", "ready", "unknown-copy", "copied", "published"} {
		t.Run(phase, func(t *testing.T) {
			var f *cloneCoordinatorFixture
			var err error
			if phase == "published" {
				var d *checkpointObjectVerificationTestDriver
				f, _, d, _, _ = checkpointObjectPublicationFixture(t)
				f.lease, _, err = f.srv.publishCheckpointObjects(t.Context(), f.lease, d)
			} else {
				var target *checkpointTargetTestDriver
				f, _, target = checkpointTargetFixture(t)
				if phase == "planned-creation" {
					for _, r := range f.lease.Operation.Resources {
						if r.Kind != "object_storage" {
							continue
						}
						var appID string
						if err = f.pool.QueryRow(t.Context(), "select app_id::text from object_buckets where id=$1", r.TargetID).Scan(&appID); err != nil {
							t.Fatal(err)
						}
						if _, err = f.store.ReserveProjectEnvironmentCloneCheckpointObjectTarget(t.Context(), f.lease, appID, r.SourceID, r.TargetID); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					target.loseCreate = phase == "unknown-creation"
					f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, target)
					if phase == "unknown-creation" && errors.Is(err, objectstorage.ErrUnavailable) {
						err = nil
					}
					if err == nil && (phase == "unknown-copy" || phase == "copied") {
						d := &checkpointObjectCopyTestDriver{checkpointTargetTestDriver: target, loseReply: phase == "unknown-copy", proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectCopyObservation{}}
						f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, d)
						if phase == "unknown-copy" && errors.Is(err, objectstorage.ErrUnavailable) {
							err = nil
						}
					}
				}
			}
			if err != nil {
				t.Fatal("prepare original dependency", err)
			}
			release, err := f.store.ProjectEnvironmentCloneCheckpointReleaseForLease(t.Context(), f.lease)
			if err != nil {
				t.Fatal(err)
			}
			p := state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan{Intent: f.lease.Operation.Resources[0], Capture: release.Plan, Receipt: &release.Receipt, Release: &release}
			compensateCheckpointObjectFixture(t, f)
			f.srv.store = f.store
			source := &checkpointSourceCompensationTestDriver{t: t, f: f}
			f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, &p, source)
			if err != nil {
				t.Fatal("source compensation", err)
			}
			capture := &checkpointCaptureRetirementTestDriver{t: t, f: f}
			if _, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture); !errors.Is(err, state.ErrConflict) || capture.calls != 0 {
				t.Fatal("unretired dependency authorized capture cleanup", err)
			}
			objects := &checkpointObjectRetirementTestDriver{t: t, f: f, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation{}}
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, objects)
			if err != nil {
				t.Fatal("retire original targets and copies", err)
			}
			if phase == "copied" {
				var id string
				var proof []byte
				if err = f.pool.QueryRow(t.Context(), "select source_bucket_id::text,observation from project_environment_clone_checkpoint_object_retirements where operation_id=$1 limit 1", f.lease.Operation.ID).Scan(&id, &proof); err != nil {
					t.Fatal(err)
				}
				capture.beforeObserve = func() {
					if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_object_retirements set observation=jsonb_set(observation,'{TargetAbsent}','false') where operation_id=$1 and source_bucket_id=$2", f.lease.Operation.ID, id); err != nil {
						t.Fatal(err)
					}
				}
				f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture)
				if !errors.Is(err, state.ErrConflict) || capture.calls != 1 {
					t.Fatal("dependency changed after dispatch accepted", err)
				}
				var retired int
				if err = f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_capture_catalogues where operation_id=$1", f.lease.Operation.ID).Scan(&retired); err != nil || retired != 0 {
					t.Fatal("invalid dependency committed catalogue retirement", retired, err)
				}
				if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_object_retirements set observation=$3 where operation_id=$1 and source_bucket_id=$2", f.lease.Operation.ID, id, proof); err != nil {
					t.Fatal(err)
				}
				capture.beforeObserve = nil
			}
			f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture)
			wantReads := 1
			if phase == "copied" {
				wantReads = 2
			}
			if err != nil || capture.calls != 1 || capture.reads != wantReads {
				t.Fatal("retire capture after authenticated dependencies", err, capture.calls, capture.reads)
			}
			capture.proof = state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{}
			f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture)
			if err != nil || capture.calls != 1 || capture.reads != wantReads {
				t.Fatal("completed replay called provider", err)
			}
			for _, r := range f.lease.Operation.Resources {
				if r.Kind != "object_storage" {
					continue
				}
				// Target retirement remains authentic after snapshot catalogue cleanup.
				in, err := f.store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, r.SourceID)
				if phase == "unknown-creation" && errors.Is(err, state.ErrNotFound) {
					continue // The second source never acquired a native intent.
				}
				if err != nil || in.State != "retired" {
					t.Fatal("dependency replay after capture cleanup", err)
				}
			}
			if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_object_retirements set observation=jsonb_set(observation,'{TargetAbsent}','false') where operation_id=$1", f.lease.Operation.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture); !errors.Is(err, state.ErrConflict) || capture.calls != 1 || capture.reads != wantReads {
				t.Fatal("altered dependency proof accepted", err)
			}
		})
	}
}
