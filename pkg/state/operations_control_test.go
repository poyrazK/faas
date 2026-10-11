// adr: 521
package state

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func operationControlFixture() (Operation, Invocation, OperationExecutionAuthority) {
	now := time.Now().UTC()
	lease, deadline := now.Add(time.Minute), now.Add(2*time.Minute)
	proof := OperationExecutionAuthority{AccountID: "account", AppID: "app", InstanceID: "instance", InvocationID: "invocation", Attempt: 2, Capability: strings.Repeat("a", 64)}
	op := Operation{OperationResponse: api.OperationResponse{ID: "operation", State: api.OperationRunning}, AccountID: proof.AccountID, AppID: proof.AppID,
		CurrentInvocationID: proof.InvocationID, ExecutionAttempt: proof.Attempt, ExecutionCapabilityDigest: operationCapabilityDigest(proof.Capability)}
	inv := Invocation{ID: proof.InvocationID, InstanceID: proof.InstanceID, State: InvocationDispatching, Attempts: proof.Attempt, LeaseExpiresAt: &lease, DeadlineAt: &deadline}
	return op, inv, proof
}

func TestOperationExecutionControlAuthorityAndDeadlineFence(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*Operation, *Invocation, *OperationExecutionAuthority)
		want error
	}{
		{"foreign account", func(_ *Operation, _ *Invocation, p *OperationExecutionAuthority) { p.AccountID = "foreign" }, ErrNotFound},
		{"foreign app", func(_ *Operation, _ *Invocation, p *OperationExecutionAuthority) { p.AppID = "foreign" }, ErrNotFound},
		{"foreign instance", func(_ *Operation, _ *Invocation, p *OperationExecutionAuthority) { p.InstanceID = "foreign" }, ErrNotFound},
		{"wrong capability", func(_ *Operation, _ *Invocation, p *OperationExecutionAuthority) {
			p.Capability = strings.Repeat("b", 64)
		}, ErrNotFound},
		{"old attempt", func(_ *Operation, _ *Invocation, p *OperationExecutionAuthority) { p.Attempt-- }, ErrOperationStaleAttempt},
		{"replacement invocation", func(op *Operation, _ *Invocation, _ *OperationExecutionAuthority) {
			op.CurrentInvocationID = "replacement"
		}, ErrOperationStaleAttempt},
		{"settled", func(op *Operation, _ *Invocation, _ *OperationExecutionAuthority) { op.State = api.OperationSucceeded }, ErrOperationStaleAttempt},
		{"expired lease", func(_ *Operation, inv *Invocation, _ *OperationExecutionAuthority) {
			past := time.Now().Add(-time.Second)
			inv.LeaseExpiresAt = &past
		}, ErrOperationStaleAttempt},
		{"expired deadline with live lease", func(_ *Operation, inv *Invocation, _ *OperationExecutionAuthority) {
			past := time.Now().Add(-time.Second)
			inv.DeadlineAt = &past
		}, ErrOperationStaleAttempt},
		{"missing deadline", func(_ *Operation, inv *Invocation, _ *OperationExecutionAuthority) { inv.DeadlineAt = nil }, ErrOperationStaleAttempt},
	} {
		t.Run(change.name, func(t *testing.T) {
			op, inv, proof := operationControlFixture()
			change.edit(&op, &inv, &proof)
			if _, err := operationExecutionControl(op, inv, proof, time.Now()); !errors.Is(err, change.want) {
				t.Fatalf("control accepted invalid authority: %v", err)
			}
		})
	}
}

func TestMemOperationExecutionControlIsReadOnlyAndCapsTimeBounds(t *testing.T) {
	m := NewMemStore()
	op, inv, proof := operationControlFixture()
	op.CancellationRequested = true
	op.ReportCount, op.LatestSequence = 7, 9
	// Even an earlier claim with a longer lease cannot extend the deadline.
	deadline := time.Now().Add(150 * time.Millisecond)
	inv.DeadlineAt = &deadline
	data := m.operationMemoryLocked()
	data.operations[op.ID], data.executions[inv.ID], m.invocations[inv.ID] = op, op.ID, inv
	read, err := m.OperationExecutionControl(t.Context(), op.ID, proof)
	if err != nil || read.OperationID != op.ID || read.InvocationID != inv.ID || !read.CancellationRequested || !read.LeaseExpiresAt.Equal(deadline) || !read.DeadlineAt.Equal(deadline) || read.PollAfterMS != api.OperationControlPollMinIntervalMS {
		t.Fatalf("control observation: %+v %v", read, err)
	}
	if !reflect.DeepEqual(op, data.operations[op.ID]) || !reflect.DeepEqual(inv, m.invocations[inv.ID]) || len(data.events[op.ID]) != 0 {
		t.Fatal("control read wrote lifecycle, quota, report or event state")
	}
	if _, err := m.OperationExecutionControl(t.Context(), "foreign-operation", proof); !errors.Is(err, ErrNotFound) {
		t.Fatal("claim read a different operation", err)
	}
}
