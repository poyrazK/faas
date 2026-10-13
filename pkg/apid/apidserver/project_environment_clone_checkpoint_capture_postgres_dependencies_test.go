//go:build !no_pg

// adr: 590
package apidserver

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func compensateCaptureDependencySourcesTest(t *testing.T, f *cloneCoordinatorFixture) {
	t.Helper()
	// Read original durable capture evidence; current source configuration never
	// supplies a replacement plan after compensation starts.
	var raw []byte
	if err := f.pool.QueryRow(t.Context(), `select jsonb_build_object(
 'Plan',plan,'Receipt',receipt,'State',state,'AdoptionSHA256',adoption_sha256,
 'RequestedAt',requested_at,'ObservationSHA256',observation_sha256)
 from project_environment_clone_checkpoint_releases where operation_id=$1`, f.lease.Operation.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var release state.ProjectEnvironmentCloneCheckpointRelease
	if err := json.Unmarshal(raw, &release); err != nil {
		t.Fatal(err)
	}
	release.RequestedAt = release.RequestedAt.UTC()
	p := state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan{Intent: f.lease.Operation.Resources[0], Capture: release.Plan, Receipt: &release.Receipt, Release: &release}
	var err error
	f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, &p, &checkpointSourceCompensationTestDriver{t: t, f: f})
	if err != nil {
		t.Fatal("source compensation", err)
	}
	objects := &checkpointObjectRetirementTestDriver{t: t, f: f, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation{}}
	f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, objects)
	if err != nil || objects.calls != 0 {
		t.Fatal("unused object reservation release", err)
	}
}

func TestPGCloneCheckpointCaptureRetirementPostgresDependencies(t *testing.T) {
	for _, phase := range []string{"absent", "reserved", "unknown", "restored", "isolation-planned", "isolation-unknown", "adopted"} {
		t.Run(phase, func(t *testing.T) {
			f, store, targets, id := checkpointPgTargetCompensationFixture(t, phase)
			compensateCaptureDependencySourcesTest(t, f)
			capture := &checkpointCaptureRetirementTestDriver{t: t, f: f}
			if phase != "absent" {
				if _, err := f.srv.retireCheckpointCapture(t.Context(), f.lease, capture); !errors.Is(err, state.ErrConflict) || capture.calls != 0 {
					t.Fatal("unretired restore authorized capture cleanup", err)
				}
			}
			var err error
			f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, targets)
			if err != nil {
				t.Fatal("target compensation", err)
			}
			wantReads := 1
			if phase == "restored" {
				var proof []byte
				if err = f.pool.QueryRow(t.Context(), "select observation from project_environment_clone_checkpoint_pg_target_compensations where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id).Scan(&proof); err != nil {
					t.Fatal(err)
				}
				capture.beforeObserve = func() {
					if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_pg_target_compensations set observation=jsonb_set(observation,'{RestorePermanentlyFenced}','false') where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id); err != nil {
						t.Fatal(err)
					}
				}
				f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture)
				if !errors.Is(err, state.ErrConflict) || capture.calls != 1 {
					t.Fatal("changed restore proof accepted after dispatch", err)
				}
				var n int
				if err = f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_capture_catalogues where operation_id=$1", f.lease.Operation.ID).Scan(&n); err != nil || n != 0 {
					t.Fatal("invalid dependency committed capture catalogue retirement", n, err)
				}
				if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_pg_target_compensations set observation=$3 where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id, proof); err != nil {
					t.Fatal(err)
				}
				capture.beforeObserve = nil
				wantReads = 2
			}
			f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture)
			if err != nil || capture.calls != 1 || capture.reads != wantReads {
				t.Fatal("retire capture after exact target proof", err, capture.calls, capture.reads)
			}
			capture.proof = state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation{}
			f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture)
			if err != nil || capture.calls != 1 || capture.reads != wantReads {
				t.Fatal("terminal replay called provider", err)
			}
			if phase != "absent" {
				in, err := store.PgStore.ProjectEnvironmentCloneCheckpointPostgresTargetCompensationForLease(t.Context(), f.lease, id)
				if err != nil || in.State != "compensated" {
					t.Fatal("target proof replay after capture retirement", err)
				}
				if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_pg_target_compensations set observation=jsonb_set(observation,'{TargetAbsent}','false') where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, id); err != nil {
					t.Fatal(err)
				}
				if _, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture); !errors.Is(err, state.ErrConflict) || capture.calls != 1 || capture.reads != wantReads {
					t.Fatal("corrupted target proof accepted on replay", err)
				}
			}
		})
	}
}

func TestPGCloneCheckpointCaptureRetirementPostgresCredentials(t *testing.T) {
	f, store, credentials, bindingID, _ := checkpointCredentialRetirementFixture(t)
	compensateCaptureDependencySourcesTest(t, f)
	var err error
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, credentials)
	if err != nil {
		t.Fatal(err)
	}
	targets := &checkpointPgTargetCompensationTestDriver{t: t, f: f}
	f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, targets)
	if err != nil {
		t.Fatal(err)
	}
	var proof []byte
	if err = f.pool.QueryRow(t.Context(), "select observation from project_environment_clone_checkpoint_pg_credential_retirements where operation_id=$1 and source_binding_id=$2", f.lease.Operation.ID, bindingID).Scan(&proof); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_pg_credential_retirements set observation=jsonb_set(observation,'{IdentityAbsent}','false') where operation_id=$1 and source_binding_id=$2", f.lease.Operation.ID, bindingID); err != nil {
		t.Fatal(err)
	}
	capture := &checkpointCaptureRetirementTestDriver{t: t, f: f}
	if _, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture); !errors.Is(err, state.ErrConflict) || capture.calls != 0 {
		t.Fatal("target proof masked changed credential proof", err)
	}
	if _, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_pg_credential_retirements set observation=$3 where operation_id=$1 and source_binding_id=$2", f.lease.Operation.ID, bindingID, proof); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.retireCheckpointCapture(t.Context(), f.lease, capture)
	if err != nil || capture.calls != 1 {
		t.Fatal("capture retirement after credential proof restored", err)
	}
	if _, err = store.ReserveProjectEnvironmentCloneCheckpointPostgresCredentialRetirement(t.Context(), f.lease, bindingID); err != nil {
		t.Fatal("credential replay after capture cleanup", err)
	}
}
