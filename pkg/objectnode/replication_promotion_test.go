package objectnode

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

func (s localReplicationSource) Fence(ctx context.Context, r FenceRequest) (PrimaryHandoff, error) {
	return s.store.FencePrimary(ctx, r)
}

func fenceTestRequest(t *testing.T, primary *Store, follower string) FenceRequest {
	t.Helper()
	local, err := ReadReplicationStatus(follower)
	if err != nil {
		t.Fatal(err)
	}
	head, err := primary.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	return FenceRequest{StreamID: head.StreamID, PrimaryID: head.NodeID, TargetID: local.NodeID, Epoch: head.Epoch}
}

func TestReplicaPlannedSwitchoverRetainsDataAndAuthority(t *testing.T) {
	primary, follower := replicationPair(t)
	source := localReplicationSource{primary}
	if _, err := PromoteReplica(t.Context(), follower, source); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("an unfenced primary authorized promotion", err)
	}
	id, rejected := uuid.NewString(), uuid.NewString()
	original, err := receiptPut(t, primary, "history", "original", id, PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	putText(t, primary, "history", "replacement")
	if _, err = receiptPut(t, primary, "history", "rejected", rejected, PutOptions{IfNoneMatch: "*"}); !errors.Is(err, ErrPrecondition) {
		t.Fatal(err)
	}
	u := newMultipart(t, primary, objectchecksum.Policy{})
	part := multipartPart(t, primary, u, 1, []byte("selected"))
	want, err := primary.Usage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	grant, err := FenceReplicaPrimary(t.Context(), follower, source)
	if err != nil || !validHandoff(grant) {
		t.Fatal(grant, err)
	}
	again, err := FenceReplicaPrimary(t.Context(), follower, source)
	if err != nil || grant != again {
		t.Fatal("fence retry changed the grant", again, err)
	}
	if _, err = PromoteReplica(t.Context(), follower, source); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("a lagging follower was promoted", err)
	}
	for operation, mutate := range map[string]func() error{
		"bucket": func() error { return primary.CreateBucket("another-bucket") },
		"delete": func() error { return primary.Delete("test-bucket", "history") },
		"put": func() error {
			_, err := primary.Put(t.Context(), "test-bucket", "late", strings.NewReader("late"), 4, PutOptions{})
			return err
		},
		"abort":  func() error { return primary.AbortMultipart(u.Bucket, u.Key, u.ID) },
		"expiry": func() error { _, err := primary.ExpireMultipart(t.Context(), time.Now().Add(time.Hour)); return err },
		"scrub":  func() error { _, err := primary.StartScrub(t.Context()); return err },
	} {
		if err = mutate(); !errors.Is(err, ErrFenced) {
			t.Fatal(operation, "was not fenced", err)
		}
	}
	if err = primary.Close(); err != nil {
		t.Fatal(err)
	}
	old, err := Open(primary.root)
	if err != nil {
		t.Fatal("fenced stream could not be exported after restart", err)
	}
	defer old.Close()
	if !errors.Is(old.CanServe(), ErrFenced) {
		t.Fatal("restart restored write authority")
	}
	catchUpTestReplica(t, old, follower)
	promoted, err := PromoteReplica(t.Context(), follower, localReplicationSource{old})
	if err != nil || promoted.Role != "primary" || promoted.Epoch != grant.Epoch+1 || promoted.PrimaryID != grant.TargetID || promoted.AppliedSequence != grant.Sequence || promoted.AppliedHash != grant.Hash || promoted.Promotion == nil || *promoted.Promotion != grant {
		t.Fatal(promoted, err)
	}
	next, err := Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if readText(t, next, "history") != "replacement" || readText(t, next, "baseline") != "seed" {
		t.Fatal("promotion changed current bytes")
	}
	proof, err := next.Receipt("test-bucket", "history", id)
	if err != nil || proof.Status != ReceiptCommitted || proof.Object.ID != original.ID {
		t.Fatal("historical receipt lost during handoff", proof, err)
	}
	negative, err := next.Receipt("test-bucket", "history", rejected)
	if err != nil || negative.Status != ReceiptRejected {
		t.Fatal(negative, err)
	}
	got, err := next.Usage(t.Context())
	if err != nil || !got.CatalogHealthy || got.CommittedBytes != want.CommittedBytes || got.Objects != want.Objects || got.MultipartBytes != want.MultipartBytes || got.MultipartParts != want.MultipartParts {
		t.Fatal("logical capacity changed during handoff", got, want, err)
	}
	if _, err = next.CompleteMultipart(t.Context(), u.Bucket, u.Key, u.ID, completion(part), nil); err != nil {
		t.Fatal("selected multipart state could not resume", err)
	}
	putText(t, next, "after", "successor")
	head, err := next.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := next.ReplicationEntry(head.StreamID, head.AppliedSequence)
	if err != nil || entry.Epoch != promoted.Epoch || head.AppliedSequence <= grant.Sequence {
		t.Fatal("successor did not journal under its epoch", entry, err)
	}
	if _, err = old.Put(t.Context(), "test-bucket", "stale", strings.NewReader("old"), 3, PutOptions{}); !errors.Is(err, ErrFenced) {
		t.Fatal("old writer accepted traffic after promotion", err)
	}
	if err = next.Close(); err != nil {
		t.Fatal(err)
	}
	// An uncertain response can be retried even when the former primary is now
	// unavailable; the durable local promotion is acknowledged without a new epoch.
	retry, err := PromoteReplica(t.Context(), follower, unavailablePromotionSource{})
	if err != nil || retry.Epoch != promoted.Epoch || retry.Promotion.ID != grant.ID {
		t.Fatal("promotion retry minted authority again", retry, err)
	}
}

type unavailablePromotionSource struct{}

func (unavailablePromotionSource) Head(context.Context) (ReplicationStatus, error) {
	return ReplicationStatus{}, io.ErrUnexpectedEOF
}
func (unavailablePromotionSource) Entry(context.Context, string, int64) (JournalEntry, error) {
	return JournalEntry{}, io.ErrUnexpectedEOF
}
func (unavailablePromotionSource) Blob(context.Context, string, string, int64, int64) (io.ReadCloser, error) {
	return nil, io.ErrUnexpectedEOF
}

func TestReplicaPromotionRejectsWrongTargetPartitionAndDamage(t *testing.T) {
	for _, failure := range []string{"wrong target", "partition", "wrong final hash", "baseline damage", "retained damage", "unfinished transfer", "symlink baseline", "hardlink baseline"} {
		t.Run(failure, func(t *testing.T) {
			primary, follower := replicationPair(t)
			putText(t, primary, "key", "payload")
			catchUpTestReplica(t, primary, follower)
			request := fenceTestRequest(t, primary, follower)
			if failure == "wrong target" {
				request.TargetID = uuid.NewString()
			}
			if _, err := primary.FencePrimary(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			var source ReplicationSource = localReplicationSource{primary}
			if failure == "partition" {
				source = unavailablePromotionSource{}
			}
			if failure == "wrong final hash" {
				source = wrongHandoffHashSource{localReplicationSource{primary}}
			}
			if failure == "baseline damage" || failure == "retained damage" || failure == "unfinished transfer" || failure == "symlink baseline" || failure == "hardlink baseline" {
				copy, err := openReplica(follower)
				if err != nil {
					t.Fatal(err)
				}
				path := ""
				switch failure {
				case "baseline damage", "symlink baseline", "hardlink baseline":
					o, err := readObject(copy.objectPath("test-bucket", "baseline"))
					if err != nil {
						t.Fatal(err)
					}
					path = filepath.Join(copy.objectPath("test-bucket", "baseline"), o.ID+".data")
				case "retained damage":
					e, err := copy.readJournal(1)
					if err != nil {
						t.Fatal(err)
					}
					path = copy.replicationPath("blobs/" + e.Blob.SHA256 + ".data")
				case "unfinished transfer":
					path = copy.replicationPath("transfer/" + nameHash("orphan") + ".part")
				}
				switch failure {
				case "symlink baseline":
					outside := filepath.Join(t.TempDir(), "outside")
					if err = os.Rename(path, outside); err == nil {
						err = os.Symlink(outside, path)
					}
				case "hardlink baseline":
					err = os.Link(path, filepath.Join(t.TempDir(), "alias"))
				default:
					body, readErr := os.ReadFile(path)
					if failure == "unfinished transfer" {
						body, readErr = []byte("orphan"), nil
					}
					if readErr != nil {
						t.Fatal(readErr)
					}
					body[0] ^= 1
					err = os.WriteFile(path, body, 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
				_ = copy.Close()
			}
			if result, err := PromoteReplica(t.Context(), follower, source); err == nil {
				t.Fatal("unsafe promotion succeeded", result)
			}
			state, err := ReadReplicationStatus(follower)
			if err != nil || state.Role != "follower" || state.Epoch != 1 {
				t.Fatal("failed promotion changed authority", state, err)
			}
		})
	}
}

type wrongHandoffHashSource struct{ localReplicationSource }

func (s wrongHandoffHashSource) Head(ctx context.Context) (ReplicationStatus, error) {
	head, err := s.localReplicationSource.Head(ctx)
	if err != nil {
		return head, err
	}
	handoff := *head.Handoff
	handoff.Hash = nameHash("wrong final checkpoint")
	handoff = sealHandoff(handoff)
	head.AppliedHash, head.ReceivedHash, head.Handoff = handoff.Hash, handoff.Hash, &handoff
	return head, nil
}

func TestReplicaFencePreflightAndBinding(t *testing.T) {
	primary, follower := replicationPair(t)
	request := fenceTestRequest(t, primary, follower)
	for _, field := range []string{"stream", "primary", "epoch", "self"} {
		bad := request
		switch field {
		case "stream":
			bad.StreamID = uuid.NewString()
		case "primary":
			bad.PrimaryID = uuid.NewString()
		case "epoch":
			bad.Epoch++
		case "self":
			bad.TargetID = bad.PrimaryID
		}
		if _, err := primary.FencePrimary(t.Context(), bad); err == nil {
			t.Fatal("stale fence request accepted", field)
		}
	}
	copy, err := openReplica(follower)
	if err != nil {
		t.Fatal(err)
	}
	o, err := readObject(copy.objectPath("test-bucket", "baseline"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(copy.objectPath("test-bucket", "baseline"), o.ID+".data"), []byte("oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = copy.Close()
	if _, err = FenceReplicaPrimary(t.Context(), follower, localReplicationSource{primary}); !errors.Is(err, ErrDigest) {
		t.Fatal("damaged target did not fail preflight", err)
	}
	if err = primary.CanServe(); err != nil {
		t.Fatal("failed preflight fenced the primary", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = primary.FencePrimary(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = primary.CanServe(); err != nil {
		t.Fatal(err)
	}
}
