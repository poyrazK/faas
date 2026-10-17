package objectqualification

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

func (l *lab) soakInterruption(ctx context.Context, index int, started time.Time, counts *loadCounts) (result SoakInterruption, err error) {
	wait, cancel := context.WithTimeout(ctx, api.ObjectQualificationRequestTimeout)
	defer cancel()
	now := time.Now()
	result.Scenario = "replication_partition"
	if index != 0 {
		result.Scenario = "follower_restart"
	}
	result.StartedAfterMS = float64(now.Sub(started)) / float64(time.Millisecond)
	result.WorkflowsBefore = counts.completed.Load()
	defer func() {
		result.DurationMS = float64(time.Since(now)) / float64(time.Millisecond)
		result.WorkflowsAfter = counts.completed.Load()
		if err != nil {
			result.Error = err.Error()
		}
	}()
	direct, err := objectnode.NewHTTPReplicationSource(l.proxy.target.Load().(string), "us-east-1", l.key, l.secret)
	if err != nil {
		return result, err
	}
	before, err := direct.Head(wait)
	if err != nil {
		return result, err
	}
	result.PublishedBefore = before.AppliedSequence
	var restore func(context.Context) error
	if index == 0 {
		l.proxy.partition.Store(true)
		restore = func(context.Context) error { l.proxy.partition.Store(false); return nil }
	} else {
		if err = l.stopFollower(wait, true); err != nil {
			return result, err
		}
		restore = l.startFollower
	}
	needsRestore := true
	defer func() {
		if needsRestore {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectQualificationStopTimeout)
			defer stop()
			err = errors.Join(err, restore(cleanup))
		}
	}()
	result.CustomerRequestsBefore = l.customerRequests.Load()
	result.CustomerActiveAtStart = l.customerActive.Load()
	timer := time.NewTimer(l.options.SoakFaultDuration)
	defer timer.Stop()
	select {
	case <-wait.Done():
		return result, wait.Err()
	case <-timer.C:
	}
	during, err := direct.Head(wait)
	if err != nil {
		return result, err
	}
	result.PublishedDuring = during.AppliedSequence
	result.CustomerRequestsDuring = l.customerRequests.Load()
	result.CustomerActiveDuring = l.customerActive.Load()
	result.LiveTraffic = result.CustomerRequestsDuring > result.CustomerRequestsBefore || result.CustomerActiveAtStart > 0 || result.CustomerActiveDuring > 0
	if result.PublishedDuring < result.PublishedBefore {
		return result, objectnode.ErrReplicationFork
	}
	if !result.LiveTraffic {
		return result, errors.New("no customer HTTP activity observed while follower was interrupted")
	}
	rejoin := time.Now()
	needsRestore = false // A failed acknowledgement must not start a duplicate worker.
	// A restarted process must outlive this interruption's deadline/cancel.
	// The captured-position wait keeps its own bounded operation context.
	if err = restore(ctx); err == nil {
		err = l.waitReplicaThrough(wait, direct, during.AppliedSequence)
	}
	result.RejoinMS = float64(time.Since(rejoin)) / float64(time.Millisecond)
	result.Recovered = err == nil
	return result, err
}

// Use a finite captured position while writers continue advancing the head.
// Requiring a moving head to be exactly caught up would turn load into starvation.
func (l *lab) waitReplicaThrough(ctx context.Context, source objectnode.ReplicationSource, sequence int64) error {
	return poll(ctx, func() (bool, error) {
		head, err := source.Head(ctx)
		if err != nil {
			return false, err
		}
		if len(head.Followers) != 1 {
			return false, errors.New("soak lost its registered follower")
		}
		ready := head.Followers[0].Sequence >= sequence
		if l.mode == objectnode.DurabilityReplica {
			ready = ready && head.Durability != nil && head.Durability.ConfirmedSequence >= sequence
		}
		return ready, nil
	})
}
