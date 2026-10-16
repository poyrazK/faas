package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type runtimeResultBytes []byte

func (r runtimeResultBytes) SealedJobsResult(context.Context, JobsSourceRequest) ([]byte, error) {
	return bytes.Clone(r), nil
}

func TestCIJobsRuntimeNativeOutcome(t *testing.T) {
	cases := []struct {
		name, guest, native, want string
		guestCode, nativeCode     int
		valid                     bool
	}{
		{name: "success", guest: StatusSucceeded, native: StatusSucceeded, want: StatusSucceeded, valid: true},
		{name: "later_native_failure", guest: StatusSucceeded, native: StatusFailed, nativeCode: 137, want: StatusFailed, valid: true},
		{name: "later_native_cancel", guest: StatusSucceeded, native: StatusCancelled, nativeCode: 143, want: StatusCancelled, valid: true},
		{name: "timeout_after_guest_cancel", guest: StatusCancelled, guestCode: 143, native: "timeout", nativeCode: 124, want: StatusFailed, valid: true},
		{name: "native_success_cannot_promote_guest_failure", guest: StatusFailed, guestCode: 2, native: StatusSucceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, job, blobs, result := jobsBlobFixture(t)
			result.Status, result.ExitCode = tc.guest, &tc.guestCode
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			out, err := (SealedJobsOutput{Store: runtimeResultBytes(raw), Blobs: blobs}).Collect(t.Context(), req, job, tc.native, &tc.nativeCode)
			if !tc.valid {
				if err == nil {
					t.Fatal("native success promoted a failed guest seal")
				}
				return
			}
			if err != nil || out.Status != tc.want || out.ExitCode == nil || *out.ExitCode != tc.nativeCode || len(out.Artifacts) != 1 || len(out.Logs) == 0 {
				t.Fatalf("native outcome or retained output lost: %+v %v", out, err)
			}
		})
	}
}

func runtimeFixture(t *testing.T) (*jobsHarness, Execution, RuntimeProof, RuntimeView) {
	t.Helper()
	h := newJobsHarness(t)
	row := h.tick(t, h.admit(t, "runtime"))
	instance := h.fakeInstance(t, row, "test")
	var lease string
	if err := h.pool.QueryRow(t.Context(), "SELECT lease_token::text FROM job_tasks WHERE run_id=$1", row.Jobs[0].Attempt.ID).Scan(&lease); err != nil {
		t.Fatal(err)
	}
	proof, err := h.store.IssueJobsRuntime(t.Context(), h.backend.Config, RuntimeHostProof{RunID: row.Jobs[0].Attempt.ID, InstanceID: instance, LeaseToken: lease})
	if err != nil {
		t.Fatal(err)
	}
	view, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeControl)
	if err != nil {
		t.Fatal(err)
	}
	return h, row, proof, view
}

func runtimeResult(t *testing.T, h *jobsHarness, proof RuntimeProof, view RuntimeView) JobsResultManifest {
	t.Helper()
	zero := 0
	result := JobsResultManifest{Version: 1, AccountID: view.Manifest.AccountID, ExecutionID: view.Manifest.ExecutionID, AttemptID: view.Manifest.AttemptID, CommitSHA: view.Manifest.CommitSHA, Status: StatusSucceeded, ExitCode: &zero}
	for _, step := range view.Manifest.Job.Steps {
		result.Steps = append(result.Steps, StepResult{Name: step.Name, Status: StatusSucceeded, ExitCode: &zero})
	}
	for name, raw := range map[string][]byte{"logs.bin": []byte("runtime logs"), "artifacts/reports/result.xml": []byte("runtime artifact")} {
		object := RuntimeObject{Name: name, Digest: jobsDigest(raw), Bytes: int64(len(raw))}
		key, retained, err := h.store.ReserveJobsRuntimeObject(t.Context(), proof, object)
		if err != nil || retained {
			t.Fatalf("reserve: %v", err)
		}
		if err := h.blobs.Put(t.Context(), key, bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
		if err := h.store.CommitJobsRuntimeObject(t.Context(), proof, object); err != nil {
			t.Fatal(err)
		}
		if name == "logs.bin" {
			result.Logs = SourceArchive{Key: key, Digest: object.Digest, Bytes: object.Bytes}
		} else {
			result.Artifacts = append(result.Artifacts, JobsArtifact{ArtifactRef: ArtifactRef{Path: "reports/result.xml", Digest: object.Digest, Bytes: object.Bytes}, Key: key})
		}
	}
	return result
}

func TestCIJobsRuntime(t *testing.T) {
	probe := pgtest.Open(t)
	probe.Close()
	if os.Getenv(pgtest.UseTemplateDatabase) == "" {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
	}
	t.Run("source_scope_seal_recovery", func(t *testing.T) {
		h, row, proof, view := runtimeFixture(t)
		if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeSource); err != nil {
			t.Fatal(err)
		}
		result := runtimeResult(t, h, proof, view)
		raw, _ := json.Marshal(result)
		if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
			t.Fatal(err)
		}
		if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
			t.Fatalf("lost seal ack not recoverable: %v", err)
		}
		result.Steps[0].Status = StatusSkipped
		changed, _ := json.Marshal(result)
		if err := h.store.SealJobsRuntime(t.Context(), proof, changed); err == nil {
			t.Fatal("sealed result changed")
		}
		if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeSource); !errors.Is(err, ErrRuntimeAuthority) {
			t.Fatal("sealed grant still fetched source")
		}
		h.restart()
		h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: h.blobs}
		if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status='succeeded',exit_code=0,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", proof.RunID); err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
			t.Fatal(err)
		}
		// Even a forged storage result.json is ignored in favor of the seal.
		if err := h.blobs.Put(t.Context(), view.Manifest.OutputPrefix+"result.json", strings.NewReader("forged")); err != nil {
			t.Fatal(err)
		}
		row = h.tick(t, row)
		if row.Jobs[0].Status != StatusSucceeded || len(row.Jobs[0].Attempt.Artifacts) != 1 || string(row.Jobs[0].Attempt.Logs) != "runtime logs" {
			t.Fatal("verified runtime output did not survive restart")
		}
	})
	t.Run("native_instance_capability_and_lease", func(t *testing.T) {
		h, _, proof, _ := runtimeFixture(t)
		for _, mutate := range []func(*RuntimeProof){func(p *RuntimeProof) { p.Capability = strings.Repeat("f", 64) }, func(p *RuntimeProof) { p.InstanceID = uuid.NewString() }, func(p *RuntimeProof) { p.RunID = uuid.NewString() }} {
			bad := proof
			mutate(&bad)
			if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, bad, RuntimeSource); !errors.Is(err, ErrRuntimeAuthority) {
				t.Fatalf("invalid proof accepted: %v", err)
			}
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET lease_token=$2 WHERE run_id=$1", proof.RunID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeUpload); !errors.Is(err, ErrRuntimeAuthority) {
			t.Fatal("replaced native lease kept authority")
		}
	})
	t.Run("cancel_revokes_reads_bounded_finalization", func(t *testing.T) {
		h, row, proof, view := runtimeFixture(t)
		result := runtimeResult(t, h, proof, view)
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		row = h.tick(t, row)
		if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeSource); !errors.Is(err, ErrRuntimeAuthority) {
			t.Fatal("cancelled source still readable")
		}
		if view, err := h.store.AuthorizeJobsRuntime(t.Context(), Config{}, proof, RuntimeControl); err != nil || !view.CancelRequested {
			t.Fatalf("cancel control unavailable: %v", err)
		}
		raw, _ := json.Marshal(result)
		if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
			t.Fatal(err)
		}
		h.clock.advance(api.CIJobsFinalizeSeconds + 1)
		if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, RuntimeUpload); !errors.Is(err, ErrRuntimeAuthority) {
			t.Fatal("cancel finalization window did not expire")
		}
	})
	t.Run("stop_revokes_all_guest_access", func(t *testing.T) {
		h, _, proof, _ := runtimeFixture(t)
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{RuntimeSource, RuntimeControl, RuntimeUpload} {
			if _, err := h.store.AuthorizeJobsRuntime(t.Context(), h.backend.Config, proof, action); !errors.Is(err, ErrRuntimeAuthority) {
				t.Fatalf("stopped grant authorized %s", action)
			}
		}
	})
	t.Run("one_transfer_and_restart_fencing", func(t *testing.T) {
		h, _, proof, _ := runtimeFixture(t)
		first, err := h.store.BeginJobsRuntimeTransfer(t.Context(), proof)
		if err != nil {
			t.Fatal(err)
		}
		h.restart()
		if _, err := h.store.BeginJobsRuntimeTransfer(t.Context(), proof); !errors.Is(err, ErrLeaseBusy) {
			t.Fatal("restart lost transfer admission")
		}
		h.clock.advance(2*api.CIJobsTransferSeconds + 1)
		second, err := h.store.BeginJobsRuntimeTransfer(t.Context(), proof)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.store.EndJobsRuntimeTransfer(t.Context(), proof, first); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.BeginJobsRuntimeTransfer(t.Context(), proof); !errors.Is(err, ErrLeaseBusy) {
			t.Fatal("stale release cleared newer transfer")
		}
		if err := h.store.EndJobsRuntimeTransfer(t.Context(), proof, second); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.BeginJobsRuntimeTransfer(t.Context(), proof); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unsealed_blob_does_not_publish", func(t *testing.T) {
		h, row, _, _ := runtimeFixture(t)
		h.publish(t, row, "test", StatusSucceeded, 0, nil, true, true)
		if _, err := (Engine{Store: h.store, Backend: h.backend}).Tick(t.Context(), h.account, row.ID); !errors.Is(err, ErrRuntimeAuthority) {
			t.Fatalf("unsealed guest result published: %v", err)
		}
	})
	t.Run("immutable_objects_and_budget", func(t *testing.T) {
		h, _, proof, view := runtimeFixture(t)
		object := RuntimeObject{Name: "artifacts/reports/one", Digest: jobsDigest([]byte("one")), Bytes: api.CIArtifactMaxBytes}
		if _, _, err := h.store.ReserveJobsRuntimeObject(t.Context(), proof, object); err != nil {
			t.Fatal(err)
		}
		changed := object
		changed.Digest = jobsDigest([]byte("different"))
		if _, _, err := h.store.ReserveJobsRuntimeObject(t.Context(), proof, changed); !errors.Is(err, ErrConflict) {
			t.Fatal("object reservation changed")
		}
		object.Name = "artifacts/reports/two"
		if _, _, err := h.store.ReserveJobsRuntimeObject(t.Context(), proof, object); err != nil {
			t.Fatal(err)
		}
		object.Name = "artifacts/reports/three"
		object.Bytes = 1
		if _, _, err := h.store.ReserveJobsRuntimeObject(t.Context(), proof, object); !errors.Is(err, ErrLimit) {
			t.Fatal("artifact byte budget exceeded")
		}
		for _, name := range []string{"../source/file", "result.json", "artifacts/secrets.txt", "artifacts/reports/../private"} {
			object.Name = name
			if _, _, err := h.store.ReserveJobsRuntimeObject(t.Context(), proof, object); err == nil {
				t.Fatalf("invalid object %q retained", name)
			}
		}
		zero := 0
		result := JobsResultManifest{Version: 1, AccountID: view.Manifest.AccountID, ExecutionID: view.Manifest.ExecutionID, AttemptID: proof.RunID, CommitSHA: view.Manifest.CommitSHA, Status: StatusSucceeded, ExitCode: &zero}
		raw, _ := json.Marshal(result)
		if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err == nil {
			t.Fatal("result without committed objects sealed")
		}
	})
	t.Run("flag_off_cannot_issue", func(t *testing.T) {
		h, _, proof, _ := runtimeFixture(t)
		var lease string
		if err := h.pool.QueryRow(t.Context(), "SELECT lease_token::text FROM job_tasks WHERE run_id=$1", proof.RunID).Scan(&lease); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.IssueJobsRuntime(t.Context(), Config{}, RuntimeHostProof{RunID: proof.RunID, InstanceID: proof.InstanceID, LeaseToken: lease}); !errors.Is(err, ErrRuntimeAuthority) {
			t.Fatal("flags off issued a grant")
		}
		view, err := h.store.AuthorizeJobsRuntime(t.Context(), Config{}, proof, RuntimeControl)
		if err != nil || !view.CancelRequested {
			t.Fatal("flag off did not stop existing runtime control")
		}
	})
}

func TestCIJobsRuntimeProofRedaction(t *testing.T) {
	proof := RuntimeProof{RunID: uuid.NewString(), InstanceID: uuid.NewString(), Capability: strings.Repeat("b", 64)}
	host := RuntimeHostProof{RunID: proof.RunID, InstanceID: proof.InstanceID, LeaseToken: uuid.NewString()}
	for _, value := range []any{proof, host} {
		raw, _ := json.Marshal(value)
		for _, rendered := range []string{string(raw), fmt.Sprint(value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(rendered, proof.Capability) || strings.Contains(rendered, host.LeaseToken) {
				t.Fatal("runtime authority was logged/serialized")
			}
		}
	}
	if _, err := (&PostgresStore{}).IssueJobsRuntime(context.Background(), Config{}, RuntimeHostProof{}); !errors.Is(err, ErrRuntimeAuthority) {
		t.Fatal("invalid host proof reached database")
	}
}
