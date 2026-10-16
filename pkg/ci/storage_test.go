package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

type storagePhysicalFake struct {
	mu             sync.Mutex
	data           map[string][]byte
	retired        map[string]bool
	writes         int
	retireErr      bool
	lostRetireAck  bool
	lostPublishAck bool
}

func newStoragePhysicalFake() *storagePhysicalFake {
	return &storagePhysicalFake{data: map[string][]byte{}, retired: map[string]bool{}}
}

func (p *storagePhysicalFake) Publish(ctx context.Context, key string, object RuntimeObject, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil || ctx.Err() != nil || int64(len(data)) != object.Bytes || jobsDigest(data) != object.Digest {
		return ErrStorageUnavailable
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired[key] {
		return ErrStorageUnavailable
	}
	if old, exists := p.data[key]; exists && !bytes.Equal(old, data) {
		return ErrConflict
	}
	p.data[key] = bytes.Clone(data)
	p.writes++
	if p.lostPublishAck {
		p.lostPublishAck = false
		return ErrStorageUnavailable
	}
	return nil
}

func (p *storagePhysicalFake) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, exists := p.data[key]
	if !exists || p.retired[key] || ctx.Err() != nil {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(data))), nil
}

func (p *storagePhysicalFake) Retire(ctx context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retireErr || ctx.Err() != nil {
		return ErrStorageUnavailable
	}
	delete(p.data, key)
	p.retired[key] = true
	if p.lostRetireAck {
		p.lostRetireAck = false
		return ErrStorageUnavailable
	}
	return nil
}

func managedRuntimeResult(t *testing.T, h *jobsHarness, b *ManagedBlobs, proof RuntimeProof, view RuntimeView) JobsResultManifest {
	t.Helper()
	zero := 0
	result := JobsResultManifest{Version: 1, AccountID: view.Manifest.AccountID, ExecutionID: view.Manifest.ExecutionID, AttemptID: view.Manifest.AttemptID, CommitSHA: view.Manifest.CommitSHA, Status: StatusSucceeded, ExitCode: &zero}
	for _, step := range view.Manifest.Job.Steps {
		result.Steps = append(result.Steps, StepResult{Name: step.Name, Status: StatusSucceeded, ExitCode: &zero})
	}
	for name, data := range map[string][]byte{"logs.bin": []byte("managed logs"), "artifacts/reports/managed.xml": []byte("managed report")} {
		object := RuntimeObject{Name: name, Digest: jobsDigest(data), Bytes: int64(len(data))}
		key, _, err := h.store.ReserveJobsRuntimeObject(t.Context(), proof, object)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		if err := h.store.CommitJobsRuntimeObject(t.Context(), proof, object); err != nil {
			t.Fatal(err)
		}
		if name == "logs.bin" {
			result.Logs = SourceArchive{Key: key, Digest: object.Digest, Bytes: object.Bytes}
		} else {
			result.Artifacts = append(result.Artifacts, JobsArtifact{ArtifactRef: ArtifactRef{Path: "reports/managed.xml", Digest: object.Digest, Bytes: object.Bytes}, Key: key})
		}
	}
	raw, _ := json.Marshal(result)
	if err := h.store.SealJobsRuntime(t.Context(), proof, raw); err != nil {
		t.Fatal(err)
	}
	return result
}

func stopManagedRuntime(t *testing.T, h *jobsHarness, row Execution) Execution {
	t.Helper()
	run := row.Jobs[0].Attempt.ID
	if _, err := h.pool.Exec(t.Context(), "UPDATE job_tasks SET status='succeeded',exit_code=0,finished_at=clock_timestamp(),lease_token=NULL,lease_expires_at=NULL WHERE run_id=$1", run); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=(SELECT instance_id FROM job_tasks WHERE run_id=$1)", run); err != nil {
		t.Fatal(err)
	}
	row = h.tick(t, row)
	if !terminal(row.Status) {
		if _, err := h.store.CancelExecution(t.Context(), h.account, row.ID); err != nil {
			t.Fatal(err)
		}
		row = h.tick(t, row)
	}
	return row
}

func TestCIStorage(t *testing.T) {
	probe := pgtest.Open(t)
	probe.Close()
	if os.Getenv(pgtest.UseTemplateDatabase) == "" {
		t.Setenv(pgtest.UseTemplateDatabase, "1")
	}
	t.Run("scoped_source_cache_and_frozen_artifact", func(t *testing.T) {
		h, row, proof, view := runtimeFixture(t)
		physical := newStoragePhysicalFake()
		blobs := &ManagedBlobs{Store: h.store, Physical: physical}
		if err := blobs.Put(t.Context(), view.Manifest.Source.Key, bytes.NewReader(h.stream.body)); err != nil {
			t.Fatal(err)
		}
		if err := blobs.Put(t.Context(), view.Manifest.Source.Key, bytes.NewReader(h.stream.body)); err != nil || physical.writes != 1 {
			t.Fatal("idempotent source rewrote storage")
		}
		req := JobsSourceRequest{Key: AttemptKey{AccountID: row.AccountID, ExecutionID: row.ID, AttemptID: proof.RunID, JobName: "test", Fence: 1}, CommitSHA: row.CommitSHA, Repository: h.binding.Repository, Binding: h.binding}
		cached, exists, err := blobs.SourceForAttempt(t.Context(), req)
		if err != nil || !exists || cached != view.Manifest.Source {
			t.Fatal("published exact source was not recoverable")
		}
		wrong := req
		wrong.CommitSHA = strings.Repeat("b", 40)
		if _, _, err := blobs.SourceForAttempt(t.Context(), wrong); err == nil {
			t.Fatal("source cache ignored pinned commit")
		}
		foreign := strings.Replace(view.Manifest.Source.Key, h.account, h.other, 1)
		if err := blobs.Put(t.Context(), foreign, bytes.NewReader(h.stream.body)); err == nil {
			t.Fatal("foreign source acquired a storage reservation")
		}
		result := managedRuntimeResult(t, h, blobs, proof, view)
		if _, err := h.store.Artifact(t.Context(), h.backend.Config, h.account, row.ID, "test", result.Artifacts[0].Path); !errors.Is(err, ErrNotFound) {
			t.Fatal("artifact served before native reconciliation")
		}
		h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: blobs}
		row = stopManagedRuntime(t, h, row)
		artifact, err := h.store.Artifact(t.Context(), h.backend.Config, h.account, row.ID, "test", result.Artifacts[0].Path)
		if err != nil || artifact != result.Artifacts[0] {
			t.Fatalf("frozen artifact unavailable: %v", err)
		}
		if _, err := h.store.Artifact(t.Context(), h.backend.Config, h.other, row.ID, "test", artifact.Path); !errors.Is(err, ErrNotFound) {
			t.Fatal("artifact crossed account boundary")
		}
	})
	t.Run("lost_source_publish_ack_is_recovered_without_refetch", func(t *testing.T) {
		h, row, proof, view := runtimeFixture(t)
		physical := newStoragePhysicalFake()
		physical.lostPublishAck = true
		blobs := &ManagedBlobs{Store: h.store, Physical: physical}
		if err := blobs.Put(t.Context(), view.Manifest.Source.Key, bytes.NewReader(h.stream.body)); err == nil {
			t.Fatal("synthetic lost acknowledgement was not applied")
		}
		req := JobsSourceRequest{Key: AttemptKey{AccountID: h.account, ExecutionID: row.ID, AttemptID: proof.RunID, JobName: "test", Fence: 1}, CommitSHA: row.CommitSHA, Repository: h.binding.Repository, Binding: h.binding}
		archive, ready, err := blobs.SourceForAttempt(t.Context(), req)
		if err != nil || !ready || archive != view.Manifest.Source || physical.writes != 1 {
			t.Fatalf("source publication recovery failed: %v", err)
		}
	})
	t.Run("stop_confirmation_retention_and_lost_retire_ack", func(t *testing.T) {
		h, row, proof, view := runtimeFixture(t)
		physical := newStoragePhysicalFake()
		blobs := &ManagedBlobs{Store: h.store, Physical: physical}
		result := managedRuntimeResult(t, h, blobs, proof, view)
		h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: blobs}
		row = stopManagedRuntime(t, h, row)
		h.clock.advance(api.CIStorageRetentionDays*24*3600 + 1)
		if _, err := h.store.Artifact(t.Context(), h.backend.Config, h.account, row.ID, "test", result.Artifacts[0].Path); !errors.Is(err, ErrNotFound) {
			t.Fatal("expired artifact remained downloadable")
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='running' WHERE id=$1", proof.InstanceID); err != nil {
			t.Fatal(err)
		}
		if n, err := blobs.Sweep(t.Context()); err != nil || n != 0 {
			t.Fatal("retention ignored native stop proof")
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
			t.Fatal(err)
		}
		physical.lostRetireAck = true
		if _, err := blobs.Sweep(t.Context()); err == nil {
			t.Fatal("lost retirement acknowledgement released quota")
		}
		var charged int64
		if err := h.pool.QueryRow(t.Context(), "SELECT sum(bytes) FROM ci_managed_objects WHERE state!='deleted'").Scan(&charged); err != nil || charged == 0 {
			t.Fatal("unconfirmed retirement was uncharged")
		}
		if n, err := blobs.Sweep(t.Context()); err != nil || n != 2 {
			t.Fatalf("idempotent cleanup failed: %d %v", n, err)
		}
		var grants int
		if err := h.pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_job_runtime").Scan(&grants); err != nil || grants != 0 {
			t.Fatal("reconciled runtime still blocks native instance retirement")
		}
		for key := range physical.retired {
			if err := physical.Publish(t.Context(), key, RuntimeObject{Digest: jobsDigest([]byte("late")), Bytes: 4}, strings.NewReader("late")); err == nil {
				t.Fatal("late writer resurrected retired payload")
			}
		}
	})
	t.Run("write_lease_nonce_survives_process_loss", func(t *testing.T) {
		h, _, _, view := runtimeFixture(t)
		parsed, _ := parseStorageKey(view.Manifest.Source.Key)
		object := RuntimeObject{Name: parsed.Name, Digest: view.Manifest.Source.Digest, Bytes: view.Manifest.Source.Bytes}
		first, err := h.store.reserveStorageWrite(t.Context(), view.Manifest.Source.Key, parsed, object)
		if err != nil {
			t.Fatal(err)
		}
		h.clock.advance(api.CIStorageOperationSeconds + 1)
		second, err := h.store.reserveStorageWrite(t.Context(), view.Manifest.Source.Key, parsed, object)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.store.releaseStorage(t.Context(), first); err != nil {
			t.Fatal(err)
		}
		if err := h.store.commitStorageWrite(t.Context(), first); !errors.Is(err, ErrLeaseLost) {
			t.Fatal("stale writer committed after lease replacement")
		}
		if err := h.store.commitStorageWrite(t.Context(), second); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("orphan_journal_survives_intent_deletion_and_waits_for_stop", func(t *testing.T) {
		h, row, proof, view := runtimeFixture(t)
		physical := newStoragePhysicalFake()
		blobs := &ManagedBlobs{Store: h.store, Physical: physical}
		if err := blobs.Put(t.Context(), view.Manifest.Source.Key, bytes.NewReader(h.stream.body)); err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Exec(t.Context(), "DELETE FROM ci_executions WHERE id=$1", row.ID); err != nil {
			t.Fatal(err)
		}
		var retained int
		if err := h.pool.QueryRow(t.Context(), "SELECT count(*) FROM ci_managed_objects WHERE execution_id=$1", row.ID).Scan(&retained); err != nil || retained != 1 {
			t.Fatal("intent deletion lost remote object recovery authority")
		}
		h.clock.advance(api.CIStorageRetentionDays*24*3600 + 1)
		if n, err := blobs.Sweep(t.Context()); err != nil || n != 0 {
			t.Fatal("orphan cleanup ignored live native instance")
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE instances SET state='stopped' WHERE id=$1", proof.InstanceID); err != nil {
			t.Fatal(err)
		}
		if n, err := blobs.Sweep(t.Context()); err != nil || n != 1 || len(physical.data) != 0 {
			t.Fatalf("orphan retirement did not recover: %d %v", n, err)
		}
	})
	t.Run("global_budget_counts_other_accounts_and_unconfirmed_deletes", func(t *testing.T) {
		h := newJobsHarness(t)
		row := h.tick(t, h.admit(t, "global-budget"))
		// Metadata alone exercises the global byte ceiling without allocating
		// gigabytes. Deleting payloads remain charged until provider confirmation.
		_, err := h.pool.Exec(t.Context(), `INSERT INTO ci_managed_objects(object_key,id,account_id,execution_id,attempt_id,job_id,kind,digest,bytes,state,created_at,expires_at)
 SELECT 'global/'||i,gen_random_uuid(),$1,$2,$3,$4,'artifact',$5,$6,'deleting',clock_timestamp(),clock_timestamp()+interval '7 days' FROM generate_series(1,256) i`, h.other, row.ID, row.Jobs[0].Attempt.ID, h.req.Templates["test"], "sha256:"+strings.Repeat("a", 64), api.CIArtifactMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		key := jobsObjectPrefix(AttemptKey{AccountID: h.account, ExecutionID: row.ID, AttemptID: row.Jobs[0].Attempt.ID}) + "source/" + strings.Repeat("a", 64) + ".tar.gz"
		parsed, _ := parseStorageKey(key)
		_, err = h.store.reserveStorageWrite(t.Context(), key, parsed, RuntimeObject{Name: parsed.Name, Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 1})
		var limit *LimitError
		if !errors.As(err, &limit) || limit.Resource != "global_storage_bytes" {
			t.Fatalf("global storage ceiling was bypassed: %v", err)
		}
	})
	t.Run("concurrent_account_byte_reservations", func(t *testing.T) {
		h := newJobsHarness(t)
		first := h.tick(t, h.admit(t, "budget-first"))
		second := h.tick(t, h.admit(t, "budget-second"))
		_, err := h.pool.Exec(t.Context(), `INSERT INTO ci_managed_objects(object_key,id,account_id,execution_id,attempt_id,job_id,kind,digest,bytes,state,created_at,expires_at)
 SELECT 'budget/'||i,gen_random_uuid(),$1,$2,$3,$4,'artifact',$5,$6,'pending',clock_timestamp(),clock_timestamp()+interval '7 days' FROM generate_series(1,63) i`, h.account, first.ID, first.Jobs[0].Attempt.ID, h.req.Templates["test"], "sha256:"+strings.Repeat("a", 64), api.CIArtifactMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		for _, row := range []Execution{first, second} {
			go func() {
				key := jobsObjectPrefix(AttemptKey{AccountID: h.account, ExecutionID: row.ID, AttemptID: row.Jobs[0].Attempt.ID}) + "source/" + strings.Repeat("a", 64) + ".tar.gz"
				parsed, _ := parseStorageKey(key)
				_, err := h.store.reserveStorageWrite(t.Context(), key, parsed, RuntimeObject{Name: parsed.Name, Digest: "sha256:" + strings.Repeat("a", 64), Bytes: api.CIArtifactMaxBytes})
				errs <- err
			}()
		}
		success, limited := 0, 0
		for range 2 {
			err := <-errs
			var limit *LimitError
			if err == nil {
				success++
			} else if errors.As(err, &limit) && limit.Resource == "storage_bytes" {
				limited++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || limited != 1 {
			t.Fatal("concurrent writes exceeded account byte limit")
		}
	})
	t.Run("active_execution_and_reader_lease_block_cleanup", func(t *testing.T) {
		h, row, proof, view := runtimeFixture(t)
		blobs := &ManagedBlobs{Store: h.store, Physical: newStoragePhysicalFake()}
		result := managedRuntimeResult(t, h, blobs, proof, view)
		h.clock.advance(api.CIStorageRetentionDays*24*3600 + 1)
		if n, err := blobs.Sweep(t.Context()); err != nil || n != 0 {
			t.Fatal("live execution lost recovery data")
		}
		reader, err := blobs.Get(t.Context(), result.Artifacts[0].Key)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = reader.Close() }()
		h.backend.Output = SealedJobsOutput{Store: h.store, Blobs: blobs}
		// A live reader also blocks the collector; settle with its already
		// verified snapshot, as if a controller lost the final commit response.
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		_ = stopManagedRuntime(t, h, row)
		// Reopen during the retained interval using a short read lease before
		// moving the expiry into the past, then prove cleanup skips that object.
		if _, err := h.pool.Exec(t.Context(), "UPDATE ci_managed_objects SET expires_at=$2 WHERE object_key=$1", result.Artifacts[0].Key, h.clock.now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		reader, err = blobs.Get(t.Context(), result.Artifacts[0].Key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.pool.Exec(t.Context(), "UPDATE ci_managed_objects SET expires_at=created_at+interval '1 second' WHERE object_key=$1", result.Artifacts[0].Key); err != nil {
			t.Fatal(err)
		}
		if n, err := blobs.Sweep(t.Context()); err != nil || n != 1 {
			t.Fatalf("active reader retired: %d %v", n, err)
		}
		_ = reader.Close()
		if n, err := blobs.Sweep(t.Context()); err != nil || n != 1 {
			t.Fatalf("reader cleanup not recovered: %d %v", n, err)
		}
	})
}

func TestCIStorageKeysRejectForeignNamespaces(t *testing.T) {
	for _, key := range []string{"snap/other/mem", "ci/accounts/../executions/x", "ci-v1/other/id", "ci/accounts/" + uuid.NewString() + "/executions/" + uuid.NewString() + "/attempts/" + uuid.NewString() + "/output/artifacts/../secret"} {
		if _, err := parseStorageKey(key); err == nil {
			t.Fatal("unsafe storage key accepted")
		}
	}
}

func TestCIStorageFlagsRequireExactOptIn(t *testing.T) {
	for _, value := range []string{"", "0", "true", "yes", "2", "1"} {
		cfg := ConfigFromEnv(func(key string) string {
			if key == TransportEnv || key == RetentionEnv {
				return value
			}
			return ""
		})
		if cfg.TransportEnabled != (value == "1") || cfg.RetentionEnabled != (value == "1") {
			t.Fatalf("storage flag %q did not require exact opt-in", value)
		}
		if cfg.DispatchAllows(uuid.NewString()) {
			t.Fatal("storage opt-in enabled compute admission")
		}
	}
}
