package objectqualification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectrecovery"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// These observations cover fresh real-time proofs. No clock on a storage node
// is accelerated, and no aged retirement or physical failure-domain claim follows.
type RecoveryRetentionEvidence struct {
	WindowSeconds           int64                          `json:"window_seconds"`
	LostReplyRetried        bool                           `json:"lost_reply_retried"`
	AgedRetirementExercised bool                           `json:"aged_retirement_exercised"`
	Observations            []RecoveryRetentionObservation `json:"observations"`
}

type RecoveryRetentionObservation struct {
	Stage          string    `json:"stage"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
	CleanupSteps   int64     `json:"cleanup_steps"`
	Candidates     int64     `json:"indexed_candidates"`
	ReadyJobs      int64     `json:"ready_jobs"`
	WaitingJobs    int64     `json:"waiting_jobs"`
	WaitingBefore  int64     `json:"waiting_jobs_before_delivery"`
}

type recoveryRetentionProbe struct {
	id, key, unacknowledged string
	size                    int64
	etag                    string
	acknowledgedAt          time.Time
	observations            []RecoveryRetentionObservation
}

func enableQualificationRetention(directory string) error {
	s, err := objectnode.Open(directory)
	if err != nil {
		return err
	}
	_, err = s.EnableRecoveryRetention()
	return errors.Join(err, s.Close())
}

func (l *lab) prepareRecoveryRetention(ctx context.Context) error {
	key := "qualification/retention-history"
	body := payload(l.options.Seed, key, l.options.ObjectBytes)
	id, err := l.put(ctx, key, body, false, nil)
	if err != nil {
		return err
	}
	o := &settlementObservation{id: id}
	o.drop.Store(true)
	l.proxy.settlementProbe.Store(o)
	service := l.gateway.service()
	var outcome string
	before := l.proxy.puts.Load()
	if err = service.ReconcileObjectNodeSettlements(ctx, func(_, value string) { outcome = value }); err != nil {
		return err
	}
	ack, count := o.snapshot()
	backlog, err := l.gateway.store.ObjectNodeSettlementBacklog(ctx)
	if err != nil || outcome != "unavailable" || count != 1 || ack.ID != id || backlog.Ready != 1 {
		return errors.New("lost settlement reply did not preserve a retryable terminal job")
	}
	// Advance only the in-memory central retry clock. The node's retention
	// timestamps and first-use barrier remain on the real host clock.
	l.gateway.clock.Add(int64(api.ObjectNodeSettlementRetryMin + api.ObjectQualificationSampleInterval))
	if err = service.ReconcileObjectNodeSettlements(ctx, func(_, value string) { outcome = value }); err != nil {
		return err
	}
	repeated, count := o.snapshot()
	backlog, err = l.gateway.store.ObjectNodeSettlementBacklog(ctx)
	if err != nil || outcome != "delivered" || backlog.Ready != 0 || count != 2 || !repeated.SettledAt.Equal(ack.SettledAt) || l.proxy.puts.Load() != before {
		return errors.New("settlement retry changed its timestamp, replayed bytes or failed to drain")
	}
	backend, err := l.gateway.registry.Default("us-east-1")
	if err != nil {
		return err
	}
	unacknowledged := objectrecovery.NewID()
	later := payload(l.options.Seed, key+"-unacknowledged", l.options.ObjectBytes)
	proof, err := backend.Provider.(objectstorage.TrackedObjectWriter).WriteTrackedObject(ctx, "physical", key, unacknowledged, bytes.NewReader(later), int64(len(later)), objectstorage.ObjectMetadata{ContentType: "application/octet-stream"})
	if err != nil {
		return err
	}
	l.model.set(key, expectation(later, proof.ETag))
	l.retentionProbe = &recoveryRetentionProbe{id: id, key: key, unacknowledged: unacknowledged, size: int64(len(later)), etag: proof.ETag, acknowledgedAt: ack.SettledAt}
	return l.verifyRecoveryRetention(ctx, "initial_lost_reply_retry")
}

func (l *lab) drainNodeSettlements(ctx context.Context) error {
	drain, cancel := context.WithTimeout(ctx, api.ObjectQualificationRemoteTimeout)
	defer cancel()
	service := l.gateway.service()
	for {
		if err := drain.Err(); err != nil {
			return err
		}
		backlog, err := l.gateway.store.ObjectNodeSettlementBacklog(drain)
		if err != nil || backlog.Ready == 0 {
			return err
		}
		delivered := 0
		if err = service.ReconcileObjectNodeSettlements(drain, func(_, outcome string) {
			if outcome == "delivered" {
				delivered++
			}
		}); err != nil {
			return err
		}
		if delivered == 0 {
			return errors.New("qualification settlement delivery made no progress; inspect retained ready jobs")
		}
	}
}

func (l *lab) verifyRecoveryRetention(ctx context.Context, stage string) error {
	beforeBacklog, err := l.gateway.store.ObjectNodeSettlementBacklog(ctx)
	if err != nil {
		return err
	}
	if err := l.drainNodeSettlements(ctx); err != nil {
		return err
	}
	p := l.retentionProbe
	if p == nil {
		return errors.New("qualification retention probe is missing")
	}
	row, err := l.gateway.receipt(ctx, p.id)
	if err != nil || row.Status != "completed" {
		return errors.New("retention qualification changed central terminal settlement")
	}
	backend, err := l.gateway.registry.Default("us-east-1")
	if err != nil {
		return err
	}
	_, before := l.proxy.settlementProbe.Load().snapshot()
	if err = objectstorage.AcknowledgeSettledUpload(ctx, backend.Provider, "physical", row); err != nil {
		return err
	}
	ack, attempts := l.proxy.settlementProbe.Load().snapshot()
	if attempts != before+1 || ack.ID != p.id || !ack.SettledAt.Equal(p.acknowledgedAt) {
		return errors.New("recovery did not preserve the original node acknowledgement")
	}
	proof, err := backend.Provider.(objectstorage.ObjectWriteConfirmer).ConfirmTrackedObject(ctx, "physical", p.key, p.unacknowledged, p.size)
	if err != nil || proof.ETag != p.etag {
		return errors.New("cleanup lost unacknowledged current proof")
	}
	if err = l.caughtUp(ctx); err != nil {
		return err
	}
	values, err := waitRecoveryCleanupMetrics(ctx, l.metrics.Load().(string))
	if err != nil {
		return err
	}
	backlog, err := l.gateway.store.ObjectNodeSettlementBacklog(ctx)
	if err == nil {
		if backlog.Ready != 0 || backlog.Waiting != beforeBacklog.Waiting {
			return errors.New("settlement delivery changed waiting unknown jobs or left terminal jobs ready")
		}
		p.observations = append(p.observations, RecoveryRetentionObservation{Stage: stage, AcknowledgedAt: ack.SettledAt, CleanupSteps: values["steps_total"], Candidates: values["candidates"], ReadyJobs: backlog.Ready, WaitingJobs: backlog.Waiting, WaitingBefore: beforeBacklog.Waiting})
	}
	return err
}

func waitRecoveryCleanupMetrics(ctx context.Context, endpoint string) (map[string]int64, error) {
	wait, cancel := context.WithTimeout(ctx, api.ObjectQualificationStartupTimeout)
	defer cancel()
	var values map[string]int64
	err := poll(wait, func() (bool, error) {
		var err error
		values, err = nodeMetrics(wait, endpoint, "faas_object_node_recovery_cleanup_")
		if err != nil {
			// Sampling is best effort at 200 ms. A readiness assertion can retry
			// that transient timeout within its existing 15-second budget.
			if errors.Is(err, context.DeadlineExceeded) && wait.Err() == nil {
				return false, nil
			}
			return false, err
		}
		for _, name := range []string{"enabled", "ready", "deferred", "steps_total", "candidates", "failures_total", "receipts_removed_total", "uploads_removed_total"} {
			if _, ok := values[name]; !ok {
				return false, fmt.Errorf("missing cleanup metric %s", name)
			}
		}
		if values["receipts_removed_total"] != 0 || values["uploads_removed_total"] != 0 || values["failures_total"] != 0 {
			return false, errors.New("fresh-proof cleanup reported removal or failure")
		}
		return values["enabled"] == 1 && values["ready"] == 1 && values["deferred"] == 0 && values["steps_total"] > 0 && values["candidates"] > 0, nil
	})
	return values, err
}
