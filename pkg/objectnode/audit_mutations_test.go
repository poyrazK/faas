package objectnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	bolt "go.etcd.io/bbolt"
)

func TestMutationAuditCompletesDuringSustainedWrites(t *testing.T) {
	s := testStore(t)
	for i := range 128 {
		putText(t, s, fmt.Sprintf("base-%03d", i), "abc")
	}
	u := newMultipart(t, s, objectchecksum.Policy{})
	for i := int32(1); i <= 64; i++ {
		multipartPart(t, s, u, i, []byte("p"))
	}
	if err := s.CreateBucket("churn-bucket"); err != nil {
		t.Fatal(err)
	}
	options := InventoryAuditOptions{MaxRecords: 64, StepTime: api.ObjectNodeAuditMaxStepTime, MaxPending: 1024}
	old := &usageAuditState{At: time.Now().Add(-time.Hour), Healthy: true}
	s.usageAudit.Store(old)
	started := time.Now()
	writes := 0
	maxPending := int64(0)
	for step := range 2000 {
		// Every turn includes writes, including new keys behind ordered scan cursors,
		// replacement/deletion, active parts, terminal transitions and generations.
		putText(t, s, "hot-"+strconv.Itoa(step%8), strconv.Itoa(step))
		writes++
		key := "churn-" + strconv.Itoa(step%8)
		if err := s.Delete("test-bucket", key); err != nil {
			t.Fatal(err)
		}
		putText(t, s, key, "v")
		writes += 2
		multipartPart(t, s, u, int32(step%64+1), []byte(strconv.Itoa(step)))
		writes++
		if step%15 == 0 {
			terminal := newMultipart(t, s, objectchecksum.Policy{})
			part := multipartPart(t, s, terminal, 1, []byte("done"))
			if step%30 == 0 {
				if _, err := s.CompleteMultipart(t.Context(), terminal.Bucket, terminal.Key, terminal.ID, completion(part), nil); err != nil {
					t.Fatal(err)
				}
			} else if err := s.AbortMultipart(terminal.Bucket, terminal.Key, terminal.ID); err != nil {
				t.Fatal(err)
			}
			writes += 3
		}
		if step%20 == 0 {
			if err := s.DeleteBucket("churn-bucket"); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateBucket("churn-bucket"); err != nil {
				t.Fatal(err)
			}
			writes += 2
		}
		maxPending = max(maxPending, s.InventoryAuditStatus().Pending)
		status, err := s.AuditStep(t.Context(), options)
		if err != nil {
			t.Fatal(step, status, err)
		}
		if status.StepRecords > options.MaxRecords || status.Restarts != 0 || status.Overflows != 0 {
			t.Fatal("unbounded work or discarded coverage", status)
		}
		if status.State != "complete" {
			if s.usageAudit.Load() != old {
				t.Fatal("partial work refreshed health", status)
			}
			continue
		}
		if !status.Usage.CatalogHealthy || status.Pending != 0 || status.Reconciled == 0 || status.Usage.Snapshot != s.inventorySnapshot() {
			t.Fatal(status)
		}
		full, err := s.Usage(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		got := status.Usage
		if got.Buckets != full.Buckets || got.Objects != full.Objects || got.CommittedBytes != full.CommittedBytes || got.MultipartUploads != full.MultipartUploads || got.MultipartParts != full.MultipartParts || got.MultipartBytes != full.MultipartBytes || got.Sessions != full.Sessions || got.Receipts != full.Receipts {
			t.Fatal("mixed source coverage", got, full)
		}
		if got.StartedAt.Equal(got.FinishedAt) || status.Reconciled < uint64(step) {
			t.Fatal("did not reconcile sustained mutations", status)
		}
		t.Logf("128 initial objects, 64 active parts; completed while every turn included writes: %d turns, %d mutation operations, %d reconciliations, max backlog %d, elapsed %s", step+1, writes, status.Reconciled, maxPending, time.Since(started))
		return
	}
	t.Fatal("audit starved under bounded sustained writes", s.InventoryAuditStatus())
}

func TestMutationAuditCoalescesHotKeysAndRecoversOverflow(t *testing.T) {
	s := testStore(t)
	putText(t, s, "hot", "before")
	options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime, MaxPending: 4}
	old := &usageAuditState{At: time.Now().Add(-time.Hour), Healthy: true}
	s.usageAudit.Store(old)
	if _, err := s.AuditStep(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		putText(t, s, "hot", strconv.Itoa(i))
	}
	status := s.InventoryAuditStatus()
	if status.Pending != 2 || status.Overflows != 0 {
		t.Fatal("hot identity did not coalesce", status)
	}
	for i := range 12 {
		putText(t, s, fmt.Sprintf("overflow-%d", i), "safe")
	}
	status = s.InventoryAuditStatus()
	if status.Pending > 4 || status.Overflows != 1 || s.usageAudit.Load() != old {
		t.Fatal("overflow exceeded its bound or refreshed health", status)
	}
	for i := range 3 {
		if _, err := s.AuditStep(t.Context(), options); err != nil {
			t.Fatal(err)
		}
		putText(t, s, fmt.Sprintf("during-reset-%d", i), "safe")
	}
	if s.usageAudit.Load() != old {
		t.Fatal("queue reset extended evidence")
	}
	options.MaxRecords = 32
	status = finishIncrementalAudit(t, s, options)
	if !status.Usage.CatalogHealthy || status.Restarts != 1 || status.Overflows != 1 || status.Usage.Objects != 16 || status.Pending != 0 {
		t.Fatal("gap failed to rebuild current coverage", status)
	}
	assertUsageCountersMatchSource(t, s)
}

func auditTrackingSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	rows := map[string]map[string]string{}
	if err := s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		for _, name := range append(append([]string{}, auditTables...), inventoryAuditBucket) {
			rows[name] = map[string]string{}
			if err := tx.Bucket([]byte(name)).ForEach(func(k, v []byte) error { rows[name][string(k)] = string(v); return nil }); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestMutationAuditCheckpointRollsBackLedgerAndQueue(t *testing.T) {
	for _, kind := range []string{"fsync", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", "old")
			options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
			if _, err := s.AuditStep(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			putText(t, s, "key", "new")
			before, proof, pending, health := auditTrackingSnapshot(t, s), s.auditProof, s.auditPending.Load(), s.usageAudit.Load()
			failure := errors.New("checkpoint fsync failure")
			if kind == "cancel" {
				failure = context.Canceled
			}
			s.auditCheckpoint = func() error { return failure }
			if _, err := s.AuditStep(t.Context(), DefaultInventoryAuditOptions()); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if auditTrackingSnapshot(t, s) != before || s.auditProof != proof || s.auditPending.Load() != pending || s.usageAudit.Load() != health {
				t.Fatal("failed checkpoint consumed changes or coverage")
			}
			s.auditCheckpoint = nil
			if status := finishIncrementalAudit(t, s, DefaultInventoryAuditOptions()); !status.Usage.CatalogHealthy || status.Usage.CommittedBytes != 3 {
				t.Fatal(status)
			}
		})
	}
}

func TestMutationAuditTrackingDamageFailsClosed(t *testing.T) {
	for _, kind := range []string{"queue omission", "queue forgery", "coverage forgery", "registry forgery", "registry omission", "unobserved epoch", "masked epoch"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			putText(t, s, "key", "old")
			if _, err := s.AuditStep(t.Context(), InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}); err != nil {
				t.Fatal(err)
			}
			putText(t, s, "key", "new")
			if kind == "unobserved epoch" || kind == "masked epoch" {
				s.inventoryEpoch++
				if kind == "masked epoch" {
					putText(t, s, "masking-write", "durable")
				}
			} else if err := s.catalogIndex.db.Update(func(tx *bolt.Tx) error {
				switch kind {
				case "queue omission":
					k, _ := tx.Bucket([]byte(auditDirty)).Cursor().First()
					return tx.Bucket([]byte(auditDirty)).Delete(k)
				case "queue forgery":
					k, _ := tx.Bucket([]byte(auditDirty)).Cursor().First()
					body, _ := json.Marshal(auditEnvelope{Body: json.RawMessage(`"other"`), MAC: scrubSeal("other")})
					return tx.Bucket([]byte(auditDirty)).Put(k, body)
				case "coverage forgery":
					var m auditChanges
					_, err := s.readAuditRow(tx, inventoryAuditBucket, "coverage", &m)
					if err != nil {
						return err
					}
					m.Pending = 0
					m.Bytes = 0
					_, err = s.writeAuditChanges(tx, m)
					return err // Authenticated but not the anchored metadata revision.
				case "registry forgery":
					key := "o/" + nameHash("test-bucket") + "/" + nameHash("key")
					var n auditNode
					_, err := s.readAuditRow(tx, auditSource, key, &n)
					if err != nil {
						return err
					}
					n.Meta = nameHash("forged")
					return s.putAuditRow(tx, auditSource, key, n)
				case "registry omission":
					return tx.Bucket([]byte(auditSource)).Delete([]byte("o/" + nameHash("test-bucket") + "/" + nameHash("key")))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			failed := false
			for range 1000 {
				status, err := s.AuditStep(t.Context(), DefaultInventoryAuditOptions())
				if errors.Is(err, ErrCatalogIndex) || errors.Is(err, ErrCorrupt) {
					failed = true
					break
				}
				if err != nil || status.State == "complete" {
					t.Fatal("damaged tracking became an observation", status, err)
				}
			}
			if !failed {
				t.Fatal("tracking failure was not bounded")
			}
			if _, err := s.BucketUsageCounters(t.Context(), "test-bucket"); !errors.Is(err, ErrCatalogIndex) {
				t.Fatal("tracking damage kept serving counters", err)
			}
			putText(t, s, "after", "durable")
			if readText(t, s, "after") != "durable" {
				t.Fatal("tracking failure lost source write")
			}
		})
	}
}

func TestMutationAuditKilledProcessDiscardsAllCoverage(t *testing.T) {
	const rootEnv = "FAAS_TEST_MUTATION_AUDIT_ROOT"
	if root := os.Getenv(rootEnv); root != "" {
		s, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		putText(t, s, "before-crash", "value")
		options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime, MaxPending: 4}
		if _, err = s.AuditStep(t.Context(), options); err != nil {
			t.Fatal(err)
		}
		for i := range 8 {
			putText(t, s, fmt.Sprintf("queued-%d", i), "new")
		}
		if s.InventoryAuditStatus().Overflows != 1 {
			t.Fatal("fixture has no overflow")
		}
		// Kill before Close: recover a committed checkpoint and a dirty gap.
		if err = syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		os.Exit(1)
	}
	s := testStore(t)
	root := s.root
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMutationAuditKilledProcessDiscardsAllCoverage$")
	command.Env = append(os.Environ(), rootEnv+"="+root)
	output, runErr := command.CombinedOutput()
	var killed *exec.ExitError
	if !errors.As(runErr, &killed) || killed.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("child was not killed", string(output), runErr)
	}
	next, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	status, err := next.AuditStep(t.Context(), InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime})
	if err != nil || status.Steps != 1 || status.Pending != 0 || status.Overflows != 0 || status.Restarts != 0 || status.Cycles != 0 {
		t.Fatal("restart trusted a disk checkpoint", status, err)
	}
	status = finishIncrementalAudit(t, next, DefaultInventoryAuditOptions())
	if !status.Usage.CatalogHealthy || status.Usage.Objects != 9 {
		t.Fatal(status)
	}
	if _, err = os.Stat(filepath.Join(root, catalogIndexName)); err != nil {
		t.Fatal(err)
	}
}

func TestMutationAuditRetirementAndExpiryDuringCoverage(t *testing.T) {
	for _, terminal := range []string{"committed", "rejected", "aborted", "expired"} {
		t.Run(terminal, func(t *testing.T) {
			s, now := recoveryTestStore(t)
			retired := prepareMultipartRetirement(t, s, terminal)
			active := newMultipart(t, s, objectchecksum.Policy{})
			for n := int32(1); n <= 5; n++ {
				multipartPart(t, s, active, n, []byte("part"))
			}
			options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime}
			before, err := s.AuditStep(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			*now = now.Add(api.ObjectNodeRecoveryWindow + 48*time.Hour)
			if status := cleanupStep(t, s, 1); status.UploadsRemoved != 1 {
				t.Fatal(status)
			}
			if count, err := s.ExpireMultipartStep(t.Context(), active.ExpiresAt.Add(time.Second), expiryTestOptions(8, 1)); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			result := finishIncrementalAudit(t, s, DefaultInventoryAuditOptions())
			if !result.Usage.CatalogHealthy || result.Restarts != 0 || !result.Usage.StartedAt.Equal(before.Usage.StartedAt) || result.Usage.Sessions != 1 || result.Usage.MultipartParts != 0 || result.Usage.MultipartUploads != 0 {
				t.Fatal(result)
			}
			if _, err := s.readMultipart(retired.ID); !errors.Is(err, ErrNoUpload) {
				t.Fatal(err)
			}
			assertUsageCountersMatchSource(t, s)
		})
	}
}

func TestMutationAuditTerminalDeltaDoesNotExpandPartBacklog(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	first := multipartPart(t, s, u, 1, []byte("p"))
	// Seed native records in linear work. Multipart API admission has separate
	// tests for its full-upload size check; this fixture exercises terminal deltas.
	for n := int32(2); n <= 1025; n++ {
		dir := s.partPath(u.ID, n)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, first.Object.ID+".data"), []byte("p"), 0o600); err != nil {
			t.Fatal(err)
		}
		part := first
		part.Number = n
		s.mu.Lock()
		_, err := s.writeRecord(filepath.Join(dir, "current.json"), sealPart(part))
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	assertUsageCountersMatchSource(t, s)
	options := InventoryAuditOptions{MaxRecords: 1, StepTime: api.ObjectNodeAuditMaxStepTime, MaxPending: 4}
	if _, err := s.AuditStep(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); err != nil {
		t.Fatal(err)
	}
	status := s.InventoryAuditStatus()
	if status.Pending != 3 || status.Overflows != 0 {
		t.Fatal("terminal transition expanded work by part count", status)
	}
	if err := s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		var n auditNode
		exists, err := s.readAuditRow(tx, auditSource, "u/"+u.ID, &n)
		if err != nil || !exists || n.Active || n.Parts != (auditSet{}) || n.PartBytes != 0 || tx.Bucket([]byte(auditSourceParts)).Stats().KeyN != 1025 {
			return ErrCorrupt
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	options.MaxRecords = 17
	status = finishIncrementalAudit(t, s, options)
	if !status.Usage.CatalogHealthy || status.Usage.MultipartParts != 0 || status.Usage.MultipartBytes != 0 {
		t.Fatal(status)
	}
	if err := s.catalogIndex.db.View(func(tx *bolt.Tx) error {
		if k, _ := tx.Bucket([]byte(auditSourceParts)).Cursor().First(); k != nil {
			return ErrCorrupt
		}
		return nil
	}); err != nil {
		t.Fatal("obsolete tracking parts were not reclaimed", err)
	}
}
