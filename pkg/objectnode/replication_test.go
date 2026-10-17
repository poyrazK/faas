package objectnode

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

type localReplicationSource struct{ store *Store }

func (s localReplicationSource) Head(context.Context) (ReplicationStatus, error) {
	return s.store.ReplicationStatus()
}
func (s localReplicationSource) Entry(_ context.Context, stream string, sequence int64) (JournalEntry, error) {
	return s.store.ReplicationEntry(stream, sequence)
}

type sectionCloser struct {
	io.Reader
	close func() error
}

func (s sectionCloser) Close() error { return s.close() }
func (s localReplicationSource) Blob(_ context.Context, stream, digest string, offset, length int64) (io.ReadCloser, error) {
	f, err := s.store.OpenReplicationBlob(stream, digest)
	if err != nil {
		return nil, err
	}
	return sectionCloser{Reader: io.NewSectionReader(f, offset, length), close: f.Close}, nil
}

func replicationPair(t *testing.T) (*Store, string) {
	t.Helper()
	s := testStore(t)
	putText(t, s, "baseline", "seed")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := EnableReplication(t.Context(), s.root)
	if err != nil || status.Role != "primary" || status.AppliedSequence != 0 {
		t.Fatal(status, err)
	}
	backup, follower := filepath.Join(t.TempDir(), "backup"), filepath.Join(t.TempDir(), "follower")
	info, err := Backup(t.Context(), s.root, backup)
	if err != nil || info.FormatVersion != 4 {
		t.Fatal(info, err)
	}
	if _, err = BootstrapReplica(t.Context(), backup, follower); err != nil {
		t.Fatal(err)
	}
	primary, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = primary.Close() })
	return primary, follower
}

func catchUpTestReplica(t *testing.T, primary *Store, follower string) ReplicationProgress {
	t.Helper()
	for range 100 {
		progress, err := CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: api.ObjectNodeReplicationBatch, MaxBytes: api.ObjectNodeReplicationStepBytes})
		if err != nil {
			t.Fatal(err)
		}
		if progress.CaughtUp {
			return progress
		}
	}
	t.Fatal("follower did not catch up")
	return ReplicationProgress{}
}

func followerText(t *testing.T, s *Store, key string) string {
	t.Helper()
	o, err := readObject(s.objectPath("test-bucket", key))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(s.objectPath("test-bucket", key), o.ID+".data"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestReplicationJournalCatchUpAllMutations(t *testing.T) {
	primary, follower := replicationPair(t)
	putText(t, primary, "key", "old")
	putText(t, primary, "key", "replacement")
	id, rejected := uuid.NewString(), uuid.NewString()
	original, err := receiptPut(t, primary, "receipt", "proof", id, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = receiptPut(t, primary, "receipt", "rejected", rejected, PutOptions{IfNoneMatch: "*"}); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	if err = primary.Delete("test-bucket", "receipt"); err != nil {
		t.Fatal(err)
	}
	if err = primary.CreateBucket("recreated-bucket"); err != nil {
		t.Fatal(err)
	}
	before, err := primary.bucket("recreated-bucket")
	if err != nil {
		t.Fatal(err)
	}
	if err = primary.DeleteBucket("recreated-bucket"); err != nil {
		t.Fatal(err)
	}
	if err = primary.CreateBucket("recreated-bucket"); err != nil {
		t.Fatal(err)
	}
	u := newMultipart(t, primary, objectchecksum.Policy{})
	p := multipartPart(t, primary, u, 1, []byte("multipart"))
	if _, err = primary.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(p), nil); err != nil {
		t.Fatal(err)
	}
	active := newMultipart(t, primary, objectchecksum.Policy{})
	mp := multipartPart(t, primary, active, 1, []byte("selected"))
	aborted := newMultipart(t, primary, objectchecksum.Policy{})
	multipartPart(t, primary, aborted, 1, []byte("abort"))
	if err = primary.AbortMultipart(aborted.Bucket, aborted.Key, aborted.ID); err != nil {
		t.Fatal(err)
	}
	progress := catchUpTestReplica(t, primary, follower)
	head, err := primary.ReplicationStatus()
	if err != nil || progress.Local.AppliedHash != head.AppliedHash || progress.Local.AppliedSequence != head.AppliedSequence {
		t.Fatal(progress, head, err)
	}
	if node, err := Open(follower); !errors.Is(err, ErrReplica) {
		if node != nil {
			_ = node.Close()
		}
		t.Fatal("follower could start serving", err)
	}
	copy, err := openReplica(follower)
	if err != nil {
		t.Fatal(err)
	}
	if followerText(t, copy, "baseline") != "seed" || followerText(t, copy, "key") != "multipart" {
		t.Fatal("follower bytes differ")
	}
	if _, err = readObject(copy.objectPath("test-bucket", "receipt")); !errors.Is(err, ErrNoObject) {
		t.Fatal("delete was resurrected", err)
	}
	for receipt, status := range map[string]string{id: ReceiptCommitted, rejected: ReceiptRejected} {
		r, err := copy.readReceipt(receipt)
		if err != nil || r.Status != status || receipt == id && r.Object.ID != original.ID {
			t.Fatal(r, err)
		}
	}
	after, err := copy.bucket("recreated-bucket")
	if err != nil || after.Generation == before.Generation {
		t.Fatal("bucket reincarnation lost", after, err)
	}
	part, err := copy.readPart(active.ID, 1)
	if err != nil || part.Object.ID != mp.Object.ID {
		t.Fatal("selected part changed", part, err)
	}
	if _, err = os.Stat(filepath.Join(copy.partPath(aborted.ID, 1))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("aborted bytes retained in live catalog", err)
	}
	usage, err := copy.Usage(t.Context())
	if err != nil || !usage.CatalogHealthy || usage.Warnings != 0 {
		t.Fatal(usage, err)
	}
	if err = copy.Close(); err != nil {
		t.Fatal(err)
	}
	// Repeating a complete catch-up neither reapplies history nor changes identity.
	again, err := CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20})
	if err != nil || !again.CaughtUp || again.EntriesApplied != 0 || again.BytesTransferred != 0 || again.Local.AppliedHash != progress.Local.AppliedHash {
		t.Fatal(again, err)
	}
	if err = primary.Delete("test-bucket", "key"); err != nil {
		t.Fatal(err)
	}
	catchUpTestReplica(t, primary, follower)
	copy, err = openReplica(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	if _, err = readObject(copy.objectPath("test-bucket", "key")); !errors.Is(err, ErrNoObject) {
		t.Fatal("later delete was not applied", err)
	}
}

type failingReplicationSource struct {
	localReplicationSource
	partition bool
	cut       bool
	corrupt   bool
	foreign   bool
	duplicate bool
	offsets   []int64
}

func (s *failingReplicationSource) Head(ctx context.Context) (ReplicationStatus, error) {
	if s.partition {
		return ReplicationStatus{}, io.ErrUnexpectedEOF
	}
	head, err := s.localReplicationSource.Head(ctx)
	if s.foreign {
		head.StreamID = uuid.NewString()
		head.BaseHash = replicationGenesis(head.StreamID)
	}
	return head, err
}
func (s *failingReplicationSource) Entry(ctx context.Context, stream string, sequence int64) (JournalEntry, error) {
	if s.duplicate && sequence > 1 {
		sequence--
	}
	return s.localReplicationSource.Entry(ctx, stream, sequence)
}
func (s *failingReplicationSource) Blob(ctx context.Context, stream, digest string, offset, length int64) (io.ReadCloser, error) {
	s.offsets = append(s.offsets, offset)
	r, err := s.localReplicationSource.Blob(ctx, stream, digest, offset, length)
	if err != nil {
		return nil, err
	}
	if s.corrupt {
		_ = r.Close()
		return io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), int(length)))), nil
	}
	if s.cut {
		s.cut = false
		return sectionCloser{Reader: io.LimitReader(r, max(1, length/2)), close: r.Close}, nil
	}
	return r, nil
}

func TestReplicationPartialTransferPartitionAndChecksum(t *testing.T) {
	for _, kind := range []string{"disconnect", "corruption", "budget"} {
		t.Run(kind, func(t *testing.T) {
			primary, follower := replicationPair(t)
			body := strings.Repeat("abc", 4096)
			putText(t, primary, "large", body)
			source := &failingReplicationSource{localReplicationSource: localReplicationSource{primary}, cut: kind == "disconnect", corrupt: kind == "corruption"}
			budget := int64(64 << 20)
			if kind == "budget" {
				budget = 17
			}
			progress, err := CatchUpReplica(t.Context(), follower, source, CatchUpOptions{MaxEntries: 32, MaxBytes: budget})
			if kind != "budget" && err == nil || kind == "budget" && err != nil {
				t.Fatal(progress, err)
			}
			status, statusErr := ReadReplicationStatus(follower)
			if statusErr != nil || status.AppliedSequence != 0 || status.ReceivedSequence != 1 {
				t.Fatal(status, statusErr)
			}
			source.partition = true
			if _, err = CatchUpReplica(t.Context(), follower, source, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}); err == nil {
				t.Fatal("partition looked caught up")
			}
			source.partition, source.corrupt = false, false
			progress, err = CatchUpReplica(t.Context(), follower, source, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20})
			if err != nil || !progress.CaughtUp {
				t.Fatal(progress, err)
			}
			if kind != "corruption" && (len(source.offsets) < 2 || source.offsets[1] == 0) {
				t.Fatal("transfer restarted instead of resuming", source.offsets)
			}
			copy, err := openReplica(follower)
			if err != nil {
				t.Fatal(err)
			}
			defer copy.Close()
			if followerText(t, copy, "large") != body {
				t.Fatal("transfer accepted wrong bytes")
			}
		})
	}
}

func TestReplicationRejectsForeignAndDuplicateHistory(t *testing.T) {
	primary, follower := replicationPair(t)
	putText(t, primary, "key", "one")
	catchUpTestReplica(t, primary, follower)
	putText(t, primary, "key", "two")
	for _, kind := range []string{"foreign", "duplicate"} {
		source := &failingReplicationSource{localReplicationSource: localReplicationSource{primary}, foreign: kind == "foreign", duplicate: kind == "duplicate"}
		if _, err := CatchUpReplica(t.Context(), follower, source, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}); !errors.Is(err, ErrReplicationFork) {
			t.Fatal(kind, err)
		}
		status, err := ReadReplicationStatus(follower)
		if err != nil || status.AppliedSequence != 1 {
			t.Fatal(status, err)
		}
	}
	catchUpTestReplica(t, primary, follower)
}

func TestReplicationExpiryOnlyFollowsPrimary(t *testing.T) {
	primary, follower := replicationPair(t)
	u := newMultipart(t, primary, objectchecksum.Policy{})
	multipartPart(t, primary, u, 1, []byte("part"))
	catchUpTestReplica(t, primary, follower)
	if _, err := primary.ExpireMultipart(t.Context(), time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	catchUpTestReplica(t, primary, follower)
	copy, err := openReplica(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	session, err := copy.readMultipart(u.ID)
	if err != nil || session.Status != multipartExpired || !session.ExpiresAt.Equal(u.ExpiresAt) {
		t.Fatal(session, err)
	}
}

func TestReplicationJournalDamageFailsStartup(t *testing.T) {
	for _, kind := range []string{"record", "missing record", "blob", "state"} {
		t.Run(kind, func(t *testing.T) {
			primary, _ := replicationPair(t)
			putText(t, primary, "key", "one")
			status, err := primary.ReplicationStatus()
			if err != nil {
				t.Fatal(err)
			}
			e, err := primary.ReplicationEntry(status.StreamID, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err = primary.Close(); err != nil {
				t.Fatal(err)
			}
			path := primary.replicationPath("log/" + journalName(1))
			switch kind {
			case "record":
				err = os.WriteFile(path, []byte("{}"), 0o600)
			case "missing record":
				err = os.Remove(path)
			case "blob":
				err = os.Remove(primary.replicationPath("blobs/" + e.Blob.SHA256 + ".data"))
			case "state":
				err = os.WriteFile(primary.replicationPath("state.json"), []byte("{}"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if node, err := Open(primary.root); err == nil {
				_ = node.Close()
				t.Fatal("damaged journal was ignored")
			}
		})
	}
}

func TestReplicaDeletionRefusesSymlinkParent(t *testing.T) {
	primary, follower := replicationPair(t)
	putText(t, primary, "key", "durable")
	catchUpTestReplica(t, primary, follower)
	objects := filepath.Join(follower, "buckets", nameHash("test-bucket"), "objects")
	saved := filepath.Join(follower, "saved-objects")
	if err := os.Rename(objects, saved); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	foreign := filepath.Join(outside, nameHash("key"))
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(foreign, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, objects); err != nil {
		t.Fatal(err)
	}
	if err := primary.Delete("test-bucket", "key"); err != nil {
		t.Fatal(err)
	}
	if _, err := CatchUpReplica(t.Context(), follower, localReplicationSource{primary}, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}); !errors.Is(err, ErrCorrupt) {
		t.Fatal("unsafe parent was followed", err)
	}
	body, err := os.ReadFile(sentinel)
	if err != nil || string(body) != "preserve" {
		t.Fatal("deletion escaped the node directory", err)
	}
	if err = os.Remove(objects); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(saved, objects); err != nil {
		t.Fatal(err)
	}
	catchUpTestReplica(t, primary, follower)
}
