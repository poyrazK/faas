package objectnode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
)

func expiryTestOptions(sessions, parts int) MultipartExpiryOptions {
	return MultipartExpiryOptions{MaxSessions: sessions, MaxParts: parts, StepTime: api.ObjectNodeMultipartExpiryMaxStepTime}
}

func TestIndexedMultipartExpiryBoundsPartCleanupAndRetainsProof(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "current object")
	u := newMultipart(t, s, objectchecksum.Policy{})
	for i := int32(1); i <= 5; i++ {
		multipartPart(t, s, u, i, []byte("unused part"))
	}
	now := u.ExpiresAt.Add(time.Second)
	count, err := s.ExpireMultipartStep(t.Context(), now, expiryTestOptions(32, 2))
	parts, readErr := os.ReadDir(filepath.Join(s.multipartPath(u.ID), "parts"))
	status, _ := s.MultipartInfo(u.Bucket, u.Key, u.ID)
	if err != nil || count != 1 || readErr != nil || len(parts) != 3 || status.Status != multipartExpired || readText(t, s, "key") != "current object" {
		t.Fatal(count, len(parts), status, err, readErr)
	}
	stats, _ := s.CatalogIndexStatus()
	if stats.PartsRemoved != 2 || stats.Expired != 1 {
		t.Fatal("unbounded cleanup", stats)
	}
	for range 3 {
		if n, err := s.ExpireMultipartStep(t.Context(), now, expiryTestOptions(1, 1)); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
	if _, err = os.Stat(filepath.Join(s.multipartPath(u.ID), "parts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("parts remain", err)
	}
	if got, err := s.readMultipart(u.ID); err != nil || got.Status != multipartExpired || got.Seal != sealMultipart(got).Seal {
		t.Fatal("terminal recovery proof removed", got, err)
	}
	if count, err := s.activeMultipartCount(u.Bucket, u.Generation); err != nil || count != 0 {
		t.Fatal("active count retained expired session", count, err)
	}
}

func TestIndexedMultipartExpirySkipsAssemblyAndRevalidatesFutureHint(t *testing.T) {
	s := testStore(t)
	busy := newMultipart(t, s, objectchecksum.Policy{})
	due := newMultipart(t, s, objectchecksum.Policy{})
	s.assembling[busy.ID] = true
	now := due.ExpiresAt.Add(time.Second)
	if count, err := s.ExpireMultipartStep(t.Context(), now, expiryTestOptions(3, 1)); err != nil || count != 1 {
		t.Fatal("assembly starved other due session", count, err)
	}
	if got, err := s.readMultipart(busy.ID); err != nil || got.Status != multipartActive {
		t.Fatal("assembly expired", got, err)
	}
	delete(s.assembling, busy.ID)
	if count, err := s.ExpireMultipartStep(t.Context(), now.Add(api.ObjectMultipartCleanupRetry), expiryTestOptions(3, 1)); err != nil || count != 1 {
		t.Fatal("deferred upload did not resume", count, err)
	}
	future := newMultipart(t, s, objectchecksum.Policy{})
	hint := catalogUploadHint(future, true)
	hint.Due = time.Now().Add(-time.Hour).UnixMilli()
	if err := s.changeExpiryHint(future.ID, hint); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ExpireMultipart(t.Context(), time.Now()); err != nil || count != 0 {
		t.Fatal("forged early hint expired a live upload", count, err)
	}
	if got, err := s.readMultipart(future.ID); err != nil || got.Status != multipartActive {
		t.Fatal(got, err)
	}
}

func TestIndexedMultipartHistoryDoesNotAffectIdleWorkOrAdmission(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	if err := s.AbortMultipart(u.Bucket, u.Key, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var unrelated string
	for range 2048 {
		copy := u
		copy.ReceiptID = uuid.NewString()
		copy.ID = objectmultipart.NativeUploadID(copy.ReceiptID)
		copy.Status = multipartAborted
		copy.Headers = u.Headers.Clone()
		copy.Headers.Set(MultipartMetadataHeader, copy.ReceiptID)
		if err := os.Mkdir(s.multipartPath(copy.ID), 0o700); err != nil {
			t.Fatal(err)
		}
		unrelated = filepath.Join(s.multipartPath(copy.ID), "session.json")
		if _, err := writeJSON(unrelated, sealMultipart(copy)); err != nil {
			t.Fatal(err)
		}
	}
	next, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err = os.WriteFile(unrelated, []byte("unrelated corrupt tombstone"), 0o600); err != nil {
		t.Fatal(err)
	}
	newMultipart(t, next, objectchecksum.Policy{})
	before, _ := next.CatalogIndexStatus()
	if count, err := next.ExpireMultipart(t.Context(), time.Now()); err != nil || count != 0 {
		t.Fatal("idle expiry scanned historical source", count, err)
	}
	after, _ := next.CatalogIndexStatus()
	if after.ExpiryExamined != before.ExpiryExamined {
		t.Fatal("idle expiry examined history", before, after)
	}
	t.Log("2049 terminal sessions: idle expiry examined zero source sessions; new admission succeeded")
}

func TestIndexedMultipartExpiryDefersPendingWorkAndHonorsCancellation(t *testing.T) {
	s := testStore(t)
	u := newMultipart(t, s, objectchecksum.Policy{})
	s.pending = &writeReceipt{}
	if n, err := s.ExpireMultipart(t.Context(), u.ExpiresAt); !errors.Is(err, ErrCatalogBusy) || n != 0 {
		t.Fatal(n, err)
	}
	s.pending = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if n, err := s.ExpireMultipart(ctx, u.ExpiresAt); !errors.Is(err, context.Canceled) || n != 0 {
		t.Fatal(n, err)
	}
	for _, options := range []MultipartExpiryOptions{{}, {MaxSessions: 1, MaxParts: 1, StepTime: time.Hour}, {MaxSessions: 1, MaxParts: api.ObjectNodeMultipartExpiryMaxParts + 1, StepTime: time.Millisecond}} {
		if _, err := s.ExpireMultipartStep(t.Context(), u.ExpiresAt, options); !errors.Is(err, ErrInvalid) {
			t.Fatal(options, err)
		}
	}
	for range 4 {
		newMultipart(t, s, objectchecksum.Policy{})
	}
	s.catalogCheckpoint = func(string) error { time.Sleep(2 * time.Millisecond); return nil }
	if n, err := s.ExpireMultipartStep(t.Context(), u.ExpiresAt.Add(time.Second), MultipartExpiryOptions{MaxSessions: 32, MaxParts: 32, StepTime: time.Nanosecond}); err != nil || n != 1 {
		t.Fatal("soft budget continued after first decision", n, err)
	}
}

func TestIndexedMultipartExpiryRejectsUnboundedPartTrees(t *testing.T) {
	for _, fault := range []string{"nested", "excessive", "symlinked parts"} {
		t.Run(fault, func(t *testing.T) {
			s := testStore(t)
			u := newMultipart(t, s, objectchecksum.Policy{})
			multipartPart(t, s, u, 1, []byte("keep until checked"))
			path := s.partPath(u.ID, 1)
			want := ErrCorrupt
			if fault == "symlinked parts" {
				parts := filepath.Dir(path)
				external := filepath.Join(t.TempDir(), "external")
				if err := os.Rename(parts, external); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, parts); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(external, "1")
			} else if fault == "nested" {
				if err := os.Mkdir(filepath.Join(path, "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				want = ErrCatalogBusy
				for i := range api.ObjectNodeMultipartExpiryPartFiles {
					if err := os.WriteFile(filepath.Join(path, uuid.NewString()+".data"), []byte(fmt.Sprint(i)), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := s.ExpireMultipart(t.Context(), u.ExpiresAt.Add(time.Second)); !errors.Is(err, want) {
				t.Fatal("unbounded tree accepted", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("unexpected tree removed", err)
			}
		})
	}
}

func TestIndexedMultipartPartialExpiryReplicationAndPromotion(t *testing.T) {
	primary, follower := replicationPair(t)
	u := newMultipart(t, primary, objectchecksum.Policy{})
	for i := int32(1); i <= 3; i++ {
		multipartPart(t, primary, u, i, []byte("part"))
	}
	if n, err := primary.ExpireMultipartStep(t.Context(), u.ExpiresAt.Add(time.Second), expiryTestOptions(1, 1)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if files, err := os.ReadDir(filepath.Join(primary.multipartPath(u.ID), "parts")); err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
	catchUpTestReplica(t, primary, follower)
	peer, err := OpenFollower(follower)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(follower, catalogIndexName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("follower built a serving index", err)
	}
	// The old wire protocol permits the peer to retain terminal parts until the
	// final whole-parts removal; its replicated terminal session is authority.
	if files, err := os.ReadDir(filepath.Join(peer.store.multipartPath(u.ID), "parts")); err != nil || len(files) != 3 {
		t.Fatal(files, err)
	}
	head, _ := peer.store.ReplicationStatus()
	_ = peer.Close()
	if _, err = primary.FencePrimary(t.Context(), FenceRequest{StreamID: head.StreamID, PrimaryID: head.PrimaryID, TargetID: head.NodeID, Epoch: head.Epoch}); err != nil {
		t.Fatal(err)
	}
	catchUpTestReplica(t, primary, follower)
	if _, err = PromoteReplica(t.Context(), follower, localReplicationSource{primary}); err != nil {
		t.Fatal(err)
	}
	next, err := Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err = os.Stat(filepath.Join(next.multipartPath(u.ID), "parts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("promoted source retained expired parts", err)
	}
	if page, err := next.List(t.Context(), "test-bucket", ListRequest{Limit: 10}); err != nil || len(page.Entries) != 1 || page.Entries[0].Key != "baseline" {
		t.Fatal("promotion did not rebuild listing index", page, err)
	}
	if got, err := next.readMultipart(u.ID); err != nil || got.Status != multipartExpired {
		t.Fatal("promotion lost terminal proof", got, err)
	}
	assertUsageCountersMatchSource(t, next)
}
