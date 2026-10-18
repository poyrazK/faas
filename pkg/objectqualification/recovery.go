//go:build linux

package objectqualification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (l *lab) maintenance(ctx context.Context, cycle int) error {
	if err := l.caughtUp(ctx); err != nil {
		return err
	}
	if err := l.stopFollower(ctx, false); err != nil {
		return err
	}
	if err := l.stopPrimary(ctx, false); err != nil {
		return err
	}
	cut, err := objectnode.CompactJournal(ctx, l.primaryDir, -1)
	if err != nil {
		return err
	}
	if cut.Status.BaseSequence != cut.Status.AppliedSequence || cut.After.Records != 0 || cut.After.Blobs != 0 {
		return errors.New("fully applied history was not reclaimed")
	}
	if err = l.compactFollower(ctx); err != nil {
		return err
	}
	backup := l.newPath("backup-" + strconv.Itoa(cycle))
	if _, err = objectnode.Backup(ctx, l.primaryDir, backup); err != nil {
		return err
	}
	if _, err = objectnode.VerifyBackup(ctx, backup); err != nil {
		return err
	}
	if err = verifyCorruptionDetection(ctx, backup); err != nil {
		return err
	}
	restored := l.newPath("restored-" + strconv.Itoa(cycle))
	if _, err = objectnode.RestoreBackup(ctx, backup, restored); err != nil {
		return err
	}
	if opened, openErr := objectnode.Open(restored); !errors.Is(openErr, objectnode.ErrReplica) {
		if opened != nil {
			_ = opened.Close()
		}
		return errors.New("restored backup cloned primary authority")
	}
	if err = l.startPrimary(ctx, api.ObjectUploadSpoolMinFreeBytes); err != nil {
		return err
	}
	if err = l.startFollower(ctx); err != nil {
		return err
	}
	if err = l.caughtUp(ctx); err != nil {
		return err
	}
	progress, err := objectnode.CatchUpReplica(ctx, restored, l.source, l.catchUpOptions())
	if err != nil {
		return fmt.Errorf("restored follower did not rejoin: %w", err)
	}
	if !progress.CaughtUp {
		return errors.New("restored follower did not reach the source head")
	}
	return l.verify(ctx)
}

// Corrupt only this disposable backup, require rejection, restore the exact
// byte, then verify again before using the backup for the restore drill.
func verifyCorruptionDetection(ctx context.Context, backup string) error {
	var selected string
	err := filepath.WalkDir(filepath.Join(backup, "data"), func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type().IsRegular() && filepath.Ext(path) == ".data" {
			info, err := e.Info()
			if err != nil {
				return err
			}
			if info.Size() > 0 {
				selected = path
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if selected == "" {
		return errors.New("backup corruption scenario has no data payload")
	}
	f, err := os.OpenFile(selected, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var original [1]byte
	if _, err = f.ReadAt(original[:], 0); err != nil {
		return err
	}
	damaged := [1]byte{original[0] ^ 1}
	if _, err = f.WriteAt(damaged[:], 0); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	_, verifyErr := objectnode.VerifyBackup(ctx, backup)
	if _, err = f.WriteAt(original[:], 0); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if verifyErr == nil {
		return errors.New("corrupt backup was accepted")
	}
	_, err = objectnode.VerifyBackup(ctx, backup)
	return err
}

func (l *lab) lostAcknowledgement(ctx context.Context) error {
	key := "fault/lost-ack"
	body := payload(l.options.Seed, key, l.options.ObjectBytes)
	l.proxy.dropResponse.Store(true)
	before := l.proxy.puts.Load()
	id, err := l.put(ctx, key, body, false, nil)
	if err == nil || id == "" {
		return errors.New("lost provider response did not leave a gateway receipt")
	}
	pending, err := l.gateway.receipt(ctx, id)
	if err != nil || pending.Status != "pending" || pending.WritePhase != state.ObjectUploadDispatched {
		return errors.New("lost acknowledgement settled a gateway admission")
	}
	// A later overwrite must not replace the original historical proof.
	later := payload(l.options.Seed, key+"-later", l.options.ObjectBytes)
	backend, err := l.gateway.registry.Default("us-east-1")
	if err != nil {
		return err
	}
	proof, err := backend.Provider.(objectstorage.TrackedObjectWriter).WriteTrackedObject(ctx, "physical", key, uuid.NewString(), bytes.NewReader(later), int64(len(later)), objectstorage.ObjectMetadata{ContentType: "application/octet-stream"})
	if err != nil {
		return err
	}
	l.model.set(key, expectation(later, proof.ETag))
	if err = l.gateway.recover(ctx); err != nil {
		return err
	}
	done, err := l.gateway.receipt(ctx, id)
	if err != nil || done.Status != "completed" || done.WritePhase != state.ObjectUploadSettled {
		return errors.New("historical proof did not settle a lost acknowledgement")
	}
	if l.proxy.puts.Load() != before+2 {
		return errors.New("recovery replayed an uncertain PUT")
	}
	l.model.remember(expectedReceipt{ID: id, Key: key, ETag: done.ETag, Size: int64(len(body))})
	return nil
}

func (l *lab) missingReceipt(ctx context.Context) error {
	key := "fault/missing"
	l.proxy.dropBefore.Store(true)
	before := l.proxy.puts.Load()
	id, err := l.put(ctx, key, payload(l.options.Seed, key, l.options.ObjectBytes), false, nil)
	if err == nil || id == "" {
		return errors.New("undispatched provider request did not retain an uncertain receipt")
	}
	usage, err := l.gateway.store.ObjectUsage(ctx, l.gateway.bucket.AccountID, time.Now())
	if err != nil {
		return err
	}
	if err = l.gateway.recover(ctx); err != nil {
		return err
	}
	pending, err := l.gateway.receipt(ctx, id)
	if err != nil || pending.Status != "pending" || pending.WritePhase != state.ObjectUploadDispatched {
		return errors.New("missing proof incorrectly settled an admission")
	}
	bytes, keys := l.modelTotals()
	if err = l.gateway.inventory(ctx, bytes, keys); err != nil {
		return err
	}
	after, err := l.gateway.store.ObjectUsage(ctx, l.gateway.bucket.AccountID, time.Now())
	if err != nil {
		return err
	}
	if len(usage.Buckets) != 1 || len(after.Buckets) != 1 || after.Buckets[0].GrantedBytes < usage.Buckets[0].GrantedBytes || after.Buckets[0].GrantedKeys < usage.Buckets[0].GrantedKeys {
		return errors.New("inventory or recovery released an unknown write's capacity")
	}
	if l.proxy.puts.Load() != before+1 {
		return errors.New("missing-proof recovery replayed a PUT")
	}
	l.model.set(key, nil)
	return nil
}

func (l *lab) bucketDeletionFence(ctx context.Context) error {
	key := "fault/deletion-fence"
	if _, err := l.put(ctx, key, payload(l.options.Seed, key, l.options.ObjectBytes), false, nil); err != nil {
		return err
	}
	created, err := l.gateway.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String(key)})
	if err != nil {
		return err
	}
	before := l.proxy.deletes.Load()
	_, deleteErr := l.gateway.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(key)})
	blocked := l.proxy.deletes.Load() == before
	_, abortErr := l.gateway.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String(key), UploadId: created.UploadId})
	if abortErr != nil {
		return abortErr
	}
	if !blocked || !apiCode(deleteErr, "OperationAborted") && !apiCode(deleteErr, "ServiceUnavailable") {
		return errors.New("gateway deletion did not respect an active multipart reservation")
	}
	return l.get(ctx, key, l.model.objects[key], nil)
}

func (l *lab) replicationPartition(ctx context.Context) error {
	if err := l.caughtUp(ctx); err != nil {
		return err
	}
	baseline, err := l.source.Head(ctx)
	if err != nil {
		return err
	}
	l.proxy.partition.Store(true)
	defer l.proxy.partition.Store(false)
	key := "fault/partition"
	body := payload(l.options.Seed, key, l.options.ObjectBytes)
	if l.mode == objectnode.DurabilityLocal {
		if _, err := l.put(ctx, key, body, false, nil); err != nil {
			return err
		}
	} else {
		probe, cancel := context.WithCancel(ctx)
		defer cancel()
		type response struct {
			id  string
			err error
		}
		done := make(chan response, 1)
		go func() { id, err := l.put(probe, key, body, false, nil); done <- response{id, err} }()
		if err = l.waitPublication(ctx, baseline.AppliedSequence+3); err != nil {
			cancel()
			<-done
			return err
		}
		select {
		case result := <-done:
			if result.err == nil {
				return errors.New("replica mode acknowledged a partitioned write")
			}
			return fmt.Errorf("replica request returned before publication was observed: %w", result.err)
		default:
		}
		cancel()
		result := <-done
		id := result.id
		// The gateway headers precede the body only after it returns, so a
		// client deadline may hide the ID. Find the single dispatched request.
		if id == "" {
			id = l.proxy.lastReceipt.Load().(string)
		}
		pending, err := l.gateway.receipt(ctx, id)
		if err != nil || pending.Status != "pending" || pending.WritePhase != state.ObjectUploadDispatched {
			return errors.New("partition did not retain its capacity admission")
		}
		if err = l.gateway.recoverWithDeadline(ctx); err != nil {
			return err
		}
		pending, err = l.gateway.receipt(ctx, id)
		if err != nil || pending.Status != "pending" {
			return errors.New("unconfirmed replica receipt settled early")
		}
		l.proxy.partition.Store(false)
		if err = l.observeRejoin(ctx, "replication_partition", time.Now()); err != nil {
			return err
		}
		if err = l.gateway.recover(ctx); err != nil {
			return err
		}
		settled, err := l.gateway.receipt(ctx, id)
		if err != nil || settled.Status != "completed" {
			return errors.New("partition recovery did not settle its confirmed write")
		}
		l.model.set(key, expectation(body, settled.ETag))
		l.model.remember(expectedReceipt{ID: id, Key: key, ETag: settled.ETag, Size: int64(len(body))})
	}
	l.proxy.partition.Store(false)
	if l.mode == objectnode.DurabilityLocal {
		return l.observeRejoin(ctx, "replication_partition", time.Now())
	}
	return l.caughtUp(ctx)
}

func (l *lab) waitPublication(ctx context.Context, sequence int64) error {
	direct, err := objectnode.NewHTTPReplicationSource(l.proxy.target.Load().(string), "us-east-1", l.key, l.secret)
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, api.ObjectQualificationRequestTimeout)
	defer cancel()
	return poll(wait, func() (bool, error) {
		head, err := direct.Head(wait)
		return err == nil && head.AppliedSequence >= sequence, nil
	})
}

func (g *gatewayFixture) recoverWithDeadline(ctx context.Context) error {
	probe, cancel := context.WithTimeout(ctx, api.ObjectQualificationProbeTimeout)
	defer cancel()
	err := g.recover(probe)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func (l *lab) followerCrash(ctx context.Context) error {
	if err := l.stopFollower(ctx, true); err != nil {
		return err
	}
	key := "fault/follower-crash"
	body := payload(l.options.Seed, key, l.options.ObjectBytes)
	baseline, err := l.source.Head(ctx)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	write, cancel := context.WithTimeout(ctx, api.ObjectQualificationRequestTimeout)
	defer cancel()
	go func() { _, err := l.put(write, key, body, false, nil); done <- err }()
	if err = l.waitPublication(ctx, baseline.AppliedSequence+3); err != nil {
		cancel()
		<-done
		return err
	}
	restarted := time.Now()
	if err := l.startFollower(ctx); err != nil {
		cancel()
		<-done
		return err
	}
	if err = <-done; err != nil {
		return err
	}
	if err := l.observeRejoin(ctx, "follower_restart", restarted); err != nil {
		return err
	}
	return l.verify(ctx)
}

func (l *lab) storagePressure(ctx context.Context) error {
	if err := l.stopPrimary(ctx, false); err != nil {
		return err
	}
	if err := l.startPrimary(ctx, api.MaxObjectStoragePolicyValue/2); err != nil {
		return err
	}
	usage, err := l.gateway.store.ObjectUsage(ctx, l.gateway.bucket.AccountID, time.Now())
	if err != nil {
		return err
	}
	key := "fault/space"
	id, err := l.put(ctx, key, payload(l.options.Seed, key, l.options.ObjectBytes), false, nil)
	if err == nil {
		return errors.New("node accepted a write below its configured free-space floor")
	}
	if id == "" {
		if !apiCode(err, "SlowDown") {
			return errors.New("visible storage pressure did not return SlowDown")
		}
		after, readErr := l.gateway.store.ObjectUsage(ctx, l.gateway.bucket.AccountID, time.Now())
		if readErr != nil || len(usage.Buckets) != 1 || len(after.Buckets) != 1 || usage.Buckets[0].GrantedBytes != after.Buckets[0].GrantedBytes || usage.Buckets[0].GrantedKeys != after.Buckets[0].GrantedKeys {
			return errors.New("visible storage pressure created a new capacity hold")
		}
	} else if row, readErr := l.gateway.receipt(ctx, id); readErr != nil || row.Status != "pending" {
		return errors.New("post-dispatch storage rejection was treated as negative proof")
	}
	l.model.set(key, nil)
	if err = l.stopPrimary(ctx, false); err != nil {
		return err
	}
	if err = l.startPrimary(ctx, api.ObjectUploadSpoolMinFreeBytes); err != nil {
		return err
	}
	if err = l.caughtUp(ctx); err != nil {
		return err
	}
	if err = l.gateway.recover(ctx); err != nil {
		return err
	}
	if id != "" {
		if row, readErr := l.gateway.receipt(ctx, id); readErr != nil || row.Status != "pending" {
			return errors.New("unproven storage error released a capacity reservation")
		}
	}
	return l.get(ctx, key, nil, nil)
}

func (l *lab) promote(ctx context.Context) error {
	if err := l.caughtUp(ctx); err != nil {
		return err
	}
	if err := l.stopFollower(ctx, false); err != nil {
		return err
	}
	if l.remote != nil {
		if err := l.promoteRemote(ctx); err != nil {
			return err
		}
		if _, err := l.put(ctx, "fault/promoted", payload(l.options.Seed, "promoted", l.options.ObjectBytes), false, nil); err != nil {
			return err
		}
		if err := l.gateway.recover(ctx); err != nil {
			return err
		}
		return l.caughtUp(ctx)
	}
	if _, err := objectnode.FenceReplicaPrimary(ctx, l.followerDir, l.source); err != nil {
		return err
	}
	for {
		progress, err := objectnode.CatchUpReplica(ctx, l.followerDir, l.source, l.catchUpOptions())
		if err != nil {
			return err
		}
		if progress.CaughtUp {
			break
		}
	}
	if _, err := objectnode.PromoteReplica(ctx, l.followerDir, l.source); err != nil {
		return err
	}
	if err := l.stopPrimary(ctx, false); err != nil {
		return err
	}
	l.primaryDir, l.followerDir = l.followerDir, l.newPath("successor-follower")
	seed := l.newPath("successor-seed")
	if _, err := objectnode.Backup(ctx, l.primaryDir, seed); err != nil {
		return err
	}
	if _, err := objectnode.BootstrapReplica(ctx, seed, l.followerDir); err != nil {
		return err
	}
	local, err := objectnode.ReadReplicationStatus(l.followerDir)
	if err != nil {
		return err
	}
	if _, err = objectnode.RegisterFollower(ctx, l.primaryDir, local.NodeID, local.AppliedSequence, local.AppliedHash); err != nil {
		return err
	}
	if l.mode == objectnode.DurabilityReplica {
		if _, err = objectnode.ConfigureDurability(ctx, l.primaryDir, l.mode, local.NodeID); err != nil {
			return err
		}
	}
	if err = l.startPrimary(ctx, api.ObjectUploadSpoolMinFreeBytes); err != nil {
		return err
	}
	if err = l.startFollower(ctx); err != nil {
		return err
	}
	if err = l.caughtUp(ctx); err != nil {
		return err
	}
	if _, err = l.put(ctx, "fault/promoted", payload(l.options.Seed, "promoted", l.options.ObjectBytes), false, nil); err != nil {
		return err
	}
	if err = l.gateway.recover(ctx); err != nil {
		return err
	}
	return l.caughtUp(ctx)
}

func (l *lab) primaryCrash(ctx context.Context) error {
	if err := l.stopPrimary(ctx, true); err != nil {
		return err
	}
	restarted := time.Now()
	if err := l.startPrimary(ctx, api.ObjectUploadSpoolMinFreeBytes); err != nil {
		return err
	}
	if err := l.observeRejoin(ctx, "primary_restart", restarted); err != nil {
		return err
	}
	return l.verify(ctx)
}

func (l *lab) observeRejoin(ctx context.Context, name string, started time.Time) error {
	err := l.caughtUp(ctx)
	l.recoveries = append(l.recoveries, RecoveryObservation{Scenario: name, RejoinMS: float64(time.Since(started)) / float64(time.Millisecond), CaughtUp: err == nil})
	return err
}
