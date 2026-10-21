// adr: 521
package state_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func operationControlFixture() (state.Operation, state.Invocation, state.OperationExecutionAuthority) {
	now := time.Now().UTC()
	lease, deadline := now.Add(time.Minute), now.Add(2*time.Minute)
	proof := state.OperationExecutionAuthority{AccountID: "account", AppID: "app", InstanceID: "instance", InvocationID: "invocation", Attempt: 2, Capability: strings.Repeat("a", 64)}
	op := state.Operation{OperationResponse: api.OperationResponse{ID: "operation", State: api.OperationRunning}, AccountID: proof.AccountID, AppID: proof.AppID,
		CurrentInvocationID: proof.InvocationID, ExecutionAttempt: proof.Attempt, ExecutionCapabilityDigest: state.OperationCapabilityDigest(proof.Capability)}
	inv := state.Invocation{ID: proof.InvocationID, InstanceID: proof.InstanceID, State: state.InvocationDispatching, Attempts: proof.Attempt, LeaseExpiresAt: &lease, DeadlineAt: &deadline}
	return op, inv, proof
}

func TestOperationExecutionControlAuthorityAndDeadlineFence(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*state.Operation, *state.Invocation, *state.OperationExecutionAuthority)
		want error
	}{
		{"foreign account", func(_ *state.Operation, _ *state.Invocation, p *state.OperationExecutionAuthority) {
			p.AccountID = "foreign"
		}, state.ErrNotFound},
		{"foreign app", func(_ *state.Operation, _ *state.Invocation, p *state.OperationExecutionAuthority) {
			p.AppID = "foreign"
		}, state.ErrNotFound},
		{"foreign instance", func(_ *state.Operation, _ *state.Invocation, p *state.OperationExecutionAuthority) {
			p.InstanceID = "foreign"
		}, state.ErrNotFound},
		{"wrong capability", func(_ *state.Operation, _ *state.Invocation, p *state.OperationExecutionAuthority) {
			p.Capability = strings.Repeat("b", 64)
		}, state.ErrNotFound},
		{"old attempt", func(_ *state.Operation, _ *state.Invocation, p *state.OperationExecutionAuthority) { p.Attempt-- }, state.ErrOperationStaleAttempt},
		{"replacement invocation", func(op *state.Operation, _ *state.Invocation, _ *state.OperationExecutionAuthority) {
			op.CurrentInvocationID = "replacement"
		}, state.ErrOperationStaleAttempt},
		{"settled", func(op *state.Operation, _ *state.Invocation, _ *state.OperationExecutionAuthority) {
			op.State = api.OperationSucceeded
		}, state.ErrOperationStaleAttempt},
		{"expired lease", func(_ *state.Operation, inv *state.Invocation, _ *state.OperationExecutionAuthority) {
			past := time.Now().Add(-time.Second)
			inv.LeaseExpiresAt = &past
		}, state.ErrOperationStaleAttempt},
		{"expired deadline with live lease", func(_ *state.Operation, inv *state.Invocation, _ *state.OperationExecutionAuthority) {
			past := time.Now().Add(-time.Second)
			inv.DeadlineAt = &past
		}, state.ErrOperationStaleAttempt},
		{"missing deadline", func(_ *state.Operation, inv *state.Invocation, _ *state.OperationExecutionAuthority) {
			inv.DeadlineAt = nil
		}, state.ErrOperationStaleAttempt},
	} {
		t.Run(change.name, func(t *testing.T) {
			op, inv, proof := operationControlFixture()
			change.edit(&op, &inv, &proof)
			if _, err := state.OperationExecutionControl(op, inv, proof, time.Now()); !errors.Is(err, change.want) {
				t.Fatalf("control accepted invalid authority: %v", err)
			}
		})
	}
}

func TestMemOperationExecutionControlIsReadOnlyAndCapsTimeBounds(t *testing.T) {
	m := state.NewMemStore()
	op, inv, proof := operationControlFixture()
	op.CancellationRequested = true
	op.ReportCount, op.LatestSequence = 7, 9
	// Even an earlier claim with a longer lease cannot extend the deadline.
	deadline := time.Now().Add(150 * time.Millisecond)
	inv.DeadlineAt = &deadline
	data := m.OperationMemoryLocked()
	(*data.Operations())[op.ID], (*data.Executions())[inv.ID], (*m.Invocations())[inv.ID] = op, op.ID, inv
	read, err := m.OperationExecutionControl(t.Context(), op.ID, proof)
	if err != nil || read.OperationID != op.ID || read.InvocationID != inv.ID || !read.CancellationRequested || !read.LeaseExpiresAt.Equal(deadline) || !read.DeadlineAt.Equal(deadline) || read.PollAfterMS != api.OperationControlPollMinIntervalMS {
		t.Fatalf("control observation: %+v %v", read, err)
	}
	if !reflect.DeepEqual(op, (*data.Operations())[op.ID]) || !reflect.DeepEqual(inv, (*m.Invocations())[inv.ID]) || len((*data.Events())[op.ID]) != 0 {
		t.Fatal("control read wrote lifecycle, quota, report or event state")
	}
	if _, err := m.OperationExecutionControl(t.Context(), "foreign-operation", proof); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("claim read a different operation", err)
	}
}
