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
)

type handoffWaitReader struct {
	entered chan struct{}
	resume  <-chan struct{}
	body    io.Reader
	started bool
}

func (r *handoffWaitReader) Read(body []byte) (int, error) {
	if !r.started {
		r.started = true
		close(r.entered)
		<-r.resume
	}
	return r.body.Read(body)
}

func TestReplicaFenceDrainsStreamingAndRejectsQueuedWrites(t *testing.T) {
	primary, follower := replicationPair(t)
	request := fenceTestRequest(t, primary, follower)
	entered, resume := make(chan struct{}), make(chan struct{})
	write, queued := make(chan error, 1), make(chan error, 1)
	fence := make(chan error, 1)
	go func() {
		_, err := primary.Put(t.Context(), "test-bucket", "inflight", &handoffWaitReader{entered: entered, resume: resume, body: strings.NewReader("accepted")}, 8, PutOptions{})
		write <- err
	}()
	<-entered
	go func() {
		_, err := primary.FencePrimary(t.Context(), request)
		fence <- err
	}()
	// The waiting exclusive lifecycle lock blocks later readers. Wait for that
	// condition rather than assuming a goroutine has reached it after a sleep.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for primary.life.TryRLock() {
		primary.life.RUnlock()
		select {
		case <-deadline.C:
			close(resume)
			t.Fatal("fence did not wait for the streaming upload")
		case <-tick.C:
		}
	}
	go func() {
		_, err := primary.Put(t.Context(), "test-bucket", "queued", strings.NewReader("late"), 4, PutOptions{})
		queued <- err
	}()
	select {
	case err := <-fence:
		close(resume)
		t.Fatal("fence passed an in-flight write", err)
	default:
	}
	close(resume)
	if err := <-write; err != nil {
		t.Fatal(err)
	}
	if err := <-fence; err != nil {
		t.Fatal(err)
	}
	if err := <-queued; !errors.Is(err, ErrFenced) {
		t.Fatal("queued write crossed the fence", err)
	}
	head, err := primary.ReplicationStatus()
	if err != nil || head.Handoff.Sequence != 1 {
		t.Fatal("sealed checkpoint omitted or added writes", head, err)
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
	if readText(t, next, "inflight") != "accepted" {
		t.Fatal("drained write was lost")
	}
	if _, _, err = next.Get("test-bucket", "queued"); !errors.Is(err, ErrNoObject) {
		t.Fatal(err)
	}
}

func TestReplicaFenceSelectsOneTarget(t *testing.T) {
	primary, follower := replicationPair(t)
	first := fenceTestRequest(t, primary, follower)
	second := first
	second.TargetID = uuid.NewString()
	type result struct {
		grant PrimaryHandoff
		err   error
	}
	results := make(chan result, 2)
	for _, request := range []FenceRequest{first, second} {
		go func() { grant, err := primary.FencePrimary(t.Context(), request); results <- result{grant, err} }()
	}
	wins, conflicts := 0, 0
	for range 2 {
		r := <-results
		if r.err == nil {
			wins++
		} else if errors.Is(r.err, ErrReplicationFork) {
			conflicts++
		} else {
			t.Fatal(r.err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("conflicting grants were issued", wins, conflicts)
	}
	status, err := primary.ReplicationStatus()
	if err != nil {
		t.Fatal(err)
	}
	// API callers cannot alter the Store's grant through a returned pointer.
	status.Handoff.TargetID = uuid.NewString()
	actual, err := primary.ReplicationStatus()
	if err != nil || actual.Handoff.TargetID == status.Handoff.TargetID {
		t.Fatal("status exposed mutable authority", err)
	}
}

func TestReplicaSuccessorBackupRejoinAndSecondSwitchover(t *testing.T) {
	primary, follower := replicationPair(t)
	putText(t, primary, "first", "first epoch")
	catchUpTestReplica(t, primary, follower)
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	staleBackup, stale := filepath.Join(t.TempDir(), "old-epoch-backup"), filepath.Join(t.TempDir(), "stale")
	if _, err := Backup(t.Context(), primary.root, staleBackup); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapReplica(t.Context(), staleBackup, stale); err != nil {
		t.Fatal(err)
	}
	primary, err := Open(primary.root)
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	request := fenceTestRequest(t, primary, follower)
	if _, err := primary.FencePrimary(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := PromoteReplica(t.Context(), follower, localReplicationSource{primary}); err != nil {
		t.Fatal(err)
	}
	next, err := Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	putText(t, next, "second", "second epoch")
	if err = next.Close(); err != nil {
		t.Fatal(err)
	}
	backup, rejoined := filepath.Join(t.TempDir(), "successor-backup"), filepath.Join(t.TempDir(), "old-host-reseeded")
	if _, err = Backup(t.Context(), follower, backup); err != nil {
		t.Fatal("successor backup failed", err)
	}
	if _, err = BootstrapReplica(t.Context(), backup, rejoined); err != nil {
		t.Fatal(err)
	}
	base, err := ReadReplicationStatus(rejoined)
	if err != nil || base.Epoch != 2 || base.Role != "follower" || base.NodeID == request.PrimaryID || base.NodeID == request.TargetID || base.BaseSequence != 2 {
		t.Fatal("reseed reused authority or wrong baseline", base, err)
	}
	// A follower still bound to epoch 1 cannot accept epoch 2 as the old writer.
	if err = primary.Close(); err != nil {
		t.Fatal(err)
	}
	next, err = Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, err = CatchUpReplica(t.Context(), stale, localReplicationSource{next}, CatchUpOptions{MaxEntries: 32, MaxBytes: 64 << 20}); !errors.Is(err, ErrReplicationFork) {
		t.Fatal("old authority was followed", err)
	}
	putText(t, next, "third", "new after seed")
	catchUpTestReplica(t, next, rejoined)
	if _, err = FenceReplicaPrimary(t.Context(), rejoined, localReplicationSource{next}); err != nil {
		t.Fatal(err)
	}
	if _, err = PromoteReplica(t.Context(), rejoined, localReplicationSource{next}); err != nil {
		t.Fatal(err)
	}
	third, err := Open(rejoined)
	if err != nil {
		t.Fatal("nonzero-baseline successor did not reopen", err)
	}
	defer third.Close()
	head, err := third.ReplicationStatus()
	if err != nil || head.Epoch != 3 || head.BaseSequence != 2 {
		t.Fatal(head, err)
	}
	putText(t, third, "fourth", "third epoch")
	if readText(t, third, "first") != "first epoch" || readText(t, third, "third") != "new after seed" {
		t.Fatal("rejoin lost data")
	}
	if err = next.CanServe(); !errors.Is(err, ErrFenced) {
		t.Fatal("second old writer became writable", err)
	}
	if err = third.Close(); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(t.TempDir(), "third-epoch-backup")
	if _, err = Backup(t.Context(), rejoined, final); err != nil {
		t.Fatal("nonzero-baseline primary backup failed", err)
	}
}

func TestReplicaLegacyAuthorityUpgrade(t *testing.T) {
	primary, follower := replicationPair(t)
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{primary.root, follower} {
		s, err := openReplica(directory)
		if err != nil {
			t.Fatal(err)
		}
		state := *s.replication
		state.Version, state.NodeID, state.PrimaryID, state.Epoch = 1, "", "", 0
		if err = s.saveReplicationState(state); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}
	reopened, err := Open(primary.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	catchUpTestReplica(t, reopened, follower)
	local, err := ReadReplicationStatus(follower)
	head, headErr := reopened.ReplicationStatus()
	if err != nil || headErr != nil || local.Version != 2 || head.Version != 2 || local.PrimaryID != head.NodeID || local.NodeID == head.NodeID {
		t.Fatal(local, head, err, headErr)
	}
	if _, err = FenceReplicaPrimary(t.Context(), follower, localReplicationSource{reopened}); err != nil {
		t.Fatal(err)
	}
	if _, err = PromoteReplica(t.Context(), follower, localReplicationSource{reopened}); err != nil {
		t.Fatal(err)
	}
}

func TestReplicaPromotionNeedsReachableFencedSource(t *testing.T) {
	primary, follower := replicationPair(t)
	if _, err := FenceReplicaPrimary(t.Context(), follower, localReplicationSource{primary}); err != nil {
		t.Fatal(err)
	}
	if _, err := PromoteReplica(context.Background(), follower, unavailablePromotionSource{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("partition authorized a new writer", err)
	}
	status, err := ReadReplicationStatus(follower)
	if err != nil || status.Role != "follower" {
		t.Fatal(status, err)
	}
}

func TestReplicaSwitchoverReclaimsUnpublishedCatalogTemporary(t *testing.T) {
	primary, follower := replicationPair(t)
	copy, err := openReplica(follower)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(copy.objectPath("test-bucket", "baseline"), ".tmp-"+uuid.NewString())
	if err = os.WriteFile(path, []byte("interrupted catalog publication"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = copy.Close(); err != nil {
		t.Fatal(err)
	}
	grant, err := FenceReplicaPrimary(t.Context(), follower, localReplicationSource{primary})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unpublished temporary remained", err)
	}
	if grant.Sequence != 0 {
		t.Fatal("temporary cleanup created a catalog mutation", grant)
	}
	if _, err = PromoteReplica(t.Context(), follower, localReplicationSource{primary}); err != nil {
		t.Fatal(err)
	}
	next, err := Open(follower)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if readText(t, next, "baseline") != "seed" {
		t.Fatal("temporary cleanup changed published bytes")
	}
}
