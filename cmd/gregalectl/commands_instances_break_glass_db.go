package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// nodeRolloutActorID is the operator-recovery actor a compute rollout submits
// under when it parks the guests a candidate vmmd refused to adopt
// (ADR-20261116170000). operator_recovery_submissions.actor_id references no
// account; a fixed id keeps every rollout's parks under one auditable actor
// and its request keys in one idempotency namespace.
const nodeRolloutActorID = "5f0d6a3e-8c1b-4b7a-9e2d-7a41c0de0001"

// instancesBreakGlassDB is what --break-glass-db needs from the database:
// the store that writes the recovery intent and reads it back, and the
// pg_notify apid sends after a submission so the owning schedd acts at once
// (schedd's sweep picks up a missed notification).
type instancesBreakGlassDB struct {
	store  state.Store
	notify func(ctx context.Context, channel, payload string) error
	close  func()
}

// instancesBreakGlassDBOpener is the seam tests swap for a MemStore.
var instancesBreakGlassDBOpener = openInstancesBreakGlassDB

func openInstancesBreakGlassDB() (instancesBreakGlassDB, error) {
	pool, err := openPgPoolFromEnv(context.Background())
	if err != nil {
		return instancesBreakGlassDB{}, fmt.Errorf("gregalectl instances: %w", err)
	}
	return instancesBreakGlassDB{
		store: state.NewPgStore(pool),
		notify: func(ctx context.Context, channel, payload string) error {
			_, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload)
			return err
		},
		close: pool.Close,
	}, nil
}

// forceParkBreakGlassDB submits the force-park recovery intent apid would
// write, straight to the database, and waits for the owning schedd to settle
// it. node_join runs it from the control plane (faas_join_db_operator_host),
// where it reaches the database but not apid's operator API.
func forceParkBreakGlassDB(instanceID, reason, traceID string, timeout time.Duration) int {
	if timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-park: --timeout must be positive")
		return 2
	}
	conn, err := instancesBreakGlassDBOpener()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances force-park: %v\n", err)
		return 1
	}
	defer conn.close()
	submitter, ok := conn.store.(state.OperatorRecoverySubmissionStore)
	if !ok {
		_, _ = fmt.Fprintln(osStderr, "gregalectl instances force-park: the store cannot submit recovery intents")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	in := state.OperatorRecoverySubmission{
		ActorID:    nodeRolloutActorID,
		RequestKey: fmt.Sprintf("break-glass-db:%s:%s:%d", reason, instanceID, time.Now().UnixNano()),
		Kind:       state.OperatorIntentKindForcePark,
		Target:     instanceID,
		Reason:     reason,
	}
	if traceID != "" {
		in.TraceID = &traceID
	}
	receipt, err := submitter.SubmitOperatorRecovery(ctx, in)
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl instances force-park: submit: %v\n", err)
		return 1
	}
	if conn.notify != nil {
		payload, _ := json.Marshal(map[string]string{"intent_id": receipt.IntentID, "kind": string(receipt.Kind), "target_id": receipt.TargetID})
		if err := conn.notify(ctx, db.NotifyOperatorIntent, string(payload)); err != nil {
			_, _ = fmt.Fprintf(osStderr, "gregalectl instances force-park: notify (schedd's sweep will pick the intent up): %v\n", err)
		}
	}
	_, _ = fmt.Fprintf(osStderr, "intent=%s instance=%s\n", receipt.IntentID, instanceID)
	for {
		intent, err := conn.store.GetOperatorIntent(ctx, receipt.IntentID)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			_, _ = fmt.Fprintf(osStderr, "gregalectl instances force-park: poll intent %s: %v\n", receipt.IntentID, err)
			return 1
		}
		if err == nil {
			switch intent.Status {
			case state.OperatorIntentSucceeded:
				_, _ = fmt.Fprintf(osStdout, "force-park: instance=%s intent=%s succeeded\n", instanceID, receipt.IntentID)
				return 0
			case state.OperatorIntentFailed, state.OperatorIntentCancelled, state.OperatorIntentOutcomeUnknown:
				_, _ = fmt.Fprintf(osStderr, "gregalectl instances force-park: intent %s %s: %s\n", receipt.IntentID, intent.Status, intent.Error)
				return 1
			}
		}
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintf(osStderr, "gregalectl instances force-park: intent %s did not finish within %s\n", receipt.IntentID, timeout)
			return 1
		case <-time.After(500 * time.Millisecond):
		}
	}
}
