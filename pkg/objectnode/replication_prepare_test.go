package objectnode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

func pauseReplayCopy(t *testing.T, primary *Store) (<-chan struct{}, func(), *atomic.Int64) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	var started, released sync.Once
	var calls atomic.Int64
	primary.replayCopy = func(ctx context.Context, source, destination string, size int64, buffer []byte) (string, error) {
		calls.Add(1)
		started.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return copyBackupFile(ctx, source, destination, size, buffer)
	}
	resume := func() { released.Do(func() { close(release) }) }
	t.Cleanup(resume)
	return entered, resume, &calls
}

func awaitReplayTest(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("operation blocked behind replay preparation")
		return nil
	}
}

func TestReplayPreparationLeavesCatalogAvailableAndBoundsCopies(t *testing.T) {
	primary, follower := durableReplicationPair(t)
	entered, resume, calls := pauseReplayCopy(t, primary)
	write := make(chan error, 1)
	id := uuid.NewString()
	go func() {
		_, err := primary.Put(t.Context(), "test-bucket", "new", strings.NewReader("new payload"), 11, PutOptions{Receipt: id})
		write <- err
	}()
	<-entered
	metadata := make(chan error, 1)
	go func() {
		_, file, err := primary.Get("test-bucket", "baseline")
		if err == nil {
			_ = file.Close()
			err = primary.CreateBucket("another-bucket")
		}
		metadata <- err
	}()
	if err := awaitReplayTest(t, metadata); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := primary.Put(ctx, "test-bucket", "queued", strings.NewReader("queued"), 6, PutOptions{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting preparation did not cancel", err)
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent replay copies escaped their bound", calls.Load())
	}
	resume()
	if err := awaitReplayTest(t, write); err != nil {
		t.Fatal(err)
	}
	requireReplicaPending(t, primary)
	catchUpTestReplica(t, primary, follower)
	if err := primary.WaitForReplica(t.Context()); err != nil {
		t.Fatal(err)
	}
	proof, err := primary.Receipt("test-bucket", "new", id)
	if err != nil || proof.Status != ReceiptCommitted {
		t.Fatal(proof, err)
	}
	if entries, err := os.ReadDir(filepath.Join(primary.root, "staging")); err != nil || len(entries) != 0 {
		t.Fatal("unused preparations were retained", entries, err)
	}
}

func TestReplayPreparationRechecksConditionAndBucketGeneration(t *testing.T) {
	for _, change := range []string{"condition", "generation"} {
		t.Run(change, func(t *testing.T) {
			primary, follower := replicationPair(t)
			bucket := "test-bucket"
			options := PutOptions{Receipt: uuid.NewString()}
			if change == "condition" {
				old := putText(t, primary, "key", "old")
				options.IfMatch = old.ETag
			} else {
				bucket = "race-bucket"
				if err := primary.CreateBucket(bucket); err != nil {
					t.Fatal(err)
				}
			}
			before, err := primary.RetentionStatus()
			if err != nil {
				t.Fatal(err)
			}
			entered, resume, _ := pauseReplayCopy(t, primary)
			done := make(chan error, 1)
			go func() {
				_, err := primary.Put(t.Context(), bucket, "key", strings.NewReader("new"), 3, options)
				done <- err
			}()
			<-entered
			if change == "condition" {
				if err = primary.Delete(bucket, "key"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = primary.DeleteBucket(bucket); err != nil {
					t.Fatal(err)
				}
				if err = primary.CreateBucket(bucket); err != nil {
					t.Fatal(err)
				}
			}
			resume()
			err = awaitReplayTest(t, done)
			want := ErrPrecondition
			if change == "generation" {
				want = ErrNoBucket
			}
			if !errors.Is(err, want) {
				t.Fatal("stale preparation published", err)
			}
			if _, file, err := primary.Get(bucket, "key"); !errors.Is(err, ErrNoObject) {
				if file != nil {
					_ = file.Close()
				}
				t.Fatal("rejected bytes became current", err)
			}
			after, err := primary.RetentionStatus()
			if err != nil || after.Usage.BlobBytes != before.Usage.BlobBytes || after.Usage.Blobs != before.Usage.Blobs {
				t.Fatal("rejected preparation consumed retained replay quota", before, after, err)
			}
			if change == "condition" {
				proof, err := primary.Receipt(bucket, "key", options.Receipt)
				if err != nil || proof.Status != ReceiptRejected {
					t.Fatal("negative receipt was lost", proof, err)
				}
			}
			catchUpTestReplica(t, primary, follower)
		})
	}
}

func TestReplayPreparationRejectsChangedFilesAndAliases(t *testing.T) {
	for _, change := range []string{"source", "temporary", "retained", "hardlink", "symlink"} {
		t.Run(change, func(t *testing.T) {
			primary, _ := replicationPair(t)
			if change == "retained" {
				putText(t, primary, "existing", "body")
			}
			id := uuid.NewString()
			stage := filepath.Join(primary.root, "staging", id)
			o, err := stageObject(t.Context(), stage, "new", id, strings.NewReader("body"), 4, PutOptions{})
			if err != nil {
				t.Fatal(err)
			}
			p, err := primary.prepareReplay(t.Context(), stage, o)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			var damaged string
			switch change {
			case "source":
				damaged = stage
			case "temporary":
				damaged = p.temp
			case "retained":
				damaged = primary.replicationPath("blobs/" + o.SHA256 + ".data")
			case "hardlink":
				if err = os.Link(stage, filepath.Join(t.TempDir(), "alias")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err = os.Remove(stage); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(filepath.Join(t.TempDir(), "outside"), stage); err != nil {
					t.Fatal(err)
				}
			}
			if damaged != "" {
				info, err := os.Stat(damaged)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(damaged, []byte("evil"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err = os.Chtimes(damaged, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			primary.mu.Lock()
			clear := primary.bindPreparedReplay(p)
			destination := filepath.Join(primary.root, "staging", uuid.NewString())
			err = primary.moveStagedBlob(stage, destination)
			if err == nil {
				err = p.install()
			}
			clear()
			primary.mu.Unlock()
			if err == nil {
				t.Fatal("stale file proof authorized publication", change)
			}
		})
	}
}

func TestMultipartPreparationRechecksAbortAndKeepsCompletionFrozen(t *testing.T) {
	for _, operation := range []string{"part", "complete"} {
		t.Run(operation, func(t *testing.T) {
			primary, follower := replicationPair(t)
			u := newMultipart(t, primary, objectchecksum.Policy{})
			var part MultipartPart
			if operation == "complete" {
				part = multipartPart(t, primary, u, 1, []byte("body"))
			}
			entered, resume, _ := pauseReplayCopy(t, primary)
			done := make(chan error, 1)
			go func() {
				var err error
				if operation == "part" {
					_, err = primary.UploadPart(t.Context(), u.Bucket, u.Key, u.ID, 1, strings.NewReader("body"), 4, PutOptions{})
				} else {
					_, err = primary.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil)
				}
				done <- err
			}()
			<-entered
			err := primary.AbortMultipart(u.Bucket, u.Key, u.ID)
			if operation == "complete" && !errors.Is(err, ErrUploadBusy) || operation == "part" && err != nil {
				t.Fatal("multipart lifecycle did not retain its publication boundary", operation, err)
			}
			resume()
			err = awaitReplayTest(t, done)
			if operation == "part" && !errors.Is(err, ErrNoUpload) || operation == "complete" && err != nil {
				t.Fatal(operation, err)
			}
			catchUpTestReplica(t, primary, follower)
			if operation == "complete" {
				proof, err := primary.MultipartReceipt(u.Bucket, u.Key, u.ReceiptID)
				if err != nil || proof.Status != ReceiptCommitted || proof.Object.Size != 4 {
					t.Fatal("completion proof was lost", proof, err)
				}
			}
		})
	}
}
