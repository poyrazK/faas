package toolregistry_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestDelegatedToolInvocationsMemory(t *testing.T) {
	exerciseDelegatedInvocations(t, state.NewMemStore())
}
func TestDelegatedToolInvocationsPostgres(t *testing.T) {
	exerciseDelegatedInvocations(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func exerciseDelegatedInvocations(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	f := granttest.New(t, store, "")
	grantID := uuid.NewString()
	g := api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if _, err := f.Owner.PutToolGrant(ctx, grantID, g); err != nil {
		t.Fatal(err)
	}
	in := api.DelegatedToolInvocationIntent{GrantID: grantID, Intent: api.ToolInvocationIntent{Version: g.Version, Name: g.Name, ArgumentsSHA256: toolpublication.Digest([]byte(`{}`)), AccessMode: "open", TimeoutSeconds: 10, AllowToolExecution: true}}
	_, err := f.Agent.BeginDelegatedToolInvocation(ctx, uuid.NewString(), in)
	requireGrantStatus(t, err, http.StatusNotFound)
	g.AllowInvocation = true
	in.GrantID = uuid.NewString()
	if _, err = f.Owner.PutToolGrant(ctx, in.GrantID, g); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	begin, err := f.Agent.BeginDelegatedToolInvocation(ctx, id, in)
	if err != nil || !begin.Created || begin.Record.Delegation == nil || begin.Record.Delegation.KeyID != f.AgentKey.ID || begin.Record.Delegation.ProjectID != f.ProjectA.ID {
		t.Fatalf("begin: %+v %v", begin, err)
	}
	if !begin.Record.Intent.AllowToolExecution || begin.Record.Status != "queued" {
		t.Fatal("invocation consent or phase changed")
	}
	// No deployment capability was minted for this caller.
	key, err := store.GetAPIKey(ctx, f.Account.ID, f.AgentKey.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range key.Scopes {
		if scope == api.ScopeDeployWrite || scope == api.ScopeAdmin {
			t.Fatal("delegation widened native credentials")
		}
	}
	_, err = f.Other.GetDelegatedToolInvocation(ctx, id)
	requireGrantStatus(t, err, http.StatusNotFound)
	_, err = f.Owner.GetDelegatedToolInvocation(ctx, id)
	requireGrantStatus(t, err, http.StatusNotFound)
	_, err = f.Agent.GetToolInvocation(ctx, id)
	requireGrantStatus(t, err, http.StatusForbidden)
	// The owner journal cannot bypass the delegated dispatch contract.
	_, err = f.Owner.DispatchToolInvocation(ctx, id)
	requireGrantStatus(t, err, http.StatusConflict)
	var dispatches atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			receipt, e := f.Agent.DispatchDelegatedToolInvocation(ctx, id)
			if e != nil {
				failures <- e
			} else if receipt.DispatchGranted {
				dispatches.Add(1)
			}
		})
	}
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	if dispatches.Load() != 1 {
		t.Fatal("dispatch grant was repeated")
	}
	out, err := toolinvocation.Result([]byte(`{"content":[],"structuredContent":{"value":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	finished, err := f.Agent.FinishDelegatedToolInvocation(ctx, id, out)
	if err != nil || finished.Record.Status != "completed" {
		t.Fatal("finish failed", err)
	}
	again, err := f.Agent.BeginDelegatedToolInvocation(ctx, id, in)
	if err != nil || again.Created || again.Record.Status != "completed" {
		t.Fatal("receipt recovery reopened execution", err)
	}
	changed := in
	changed.Intent.TimeoutSeconds++
	_, err = f.Agent.BeginDelegatedToolInvocation(ctx, id, changed)
	requireGrantStatus(t, err, http.StatusConflict)
	otherGrant := uuid.NewString()
	if _, err = f.Owner.PutToolGrant(ctx, otherGrant, g); err != nil {
		t.Fatal(err)
	}
	changed = in
	changed.GrantID = otherGrant
	_, err = f.Agent.BeginDelegatedToolInvocation(ctx, id, changed)
	requireGrantStatus(t, err, http.StatusConflict)
	wrong := in
	wrong.Intent.Name = "hidden"
	_, err = f.Agent.BeginDelegatedToolInvocation(ctx, uuid.NewString(), wrong)
	requireGrantStatus(t, err, http.StatusNotFound)
	wrong = in
	wrong.Intent.Version = f.PublicationB.ID
	_, err = f.Agent.BeginDelegatedToolInvocation(ctx, uuid.NewString(), wrong)
	requireGrantStatus(t, err, http.StatusNotFound)
	queuedID := uuid.NewString()
	if _, err = f.Agent.BeginDelegatedToolInvocation(ctx, queuedID, in); err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.Agent.CancelDelegatedToolInvocation(ctx, queuedID)
	if err != nil || cancelled.Record.Status != "cancelled" {
		t.Fatal("queued cancellation failed", err)
	}
	noCall, err := f.Agent.DispatchDelegatedToolInvocation(ctx, queuedID)
	if err != nil || noCall.DispatchGranted {
		t.Fatal("cancelled invocation dispatched", err)
	}
	lostID := uuid.NewString()
	if _, err = f.Agent.BeginDelegatedToolInvocation(ctx, lostID, in); err != nil {
		t.Fatal(err)
	}
	f.LoseDispatch = true
	_, err = f.Agent.DispatchDelegatedToolInvocation(ctx, lostID)
	requireGrantStatus(t, err, http.StatusServiceUnavailable)
	f.LoseDispatch = false
	recovered, err := f.Agent.GetDelegatedToolInvocation(ctx, lostID)
	if err != nil || recovered.Status != "running" {
		t.Fatal("lost dispatch state was not retained", err)
	}
	noCall, err = f.Agent.DispatchDelegatedToolInvocation(ctx, lostID)
	if err != nil || noCall.DispatchGranted {
		t.Fatal("lost response reopened dispatch", err)
	}
	// Revocation applies even though the fixture continues authenticating its
	// cached key copy. Both runtime-data reads and mutations are refused.
	if _, err = f.Owner.RevokeToolGrant(ctx, in.GrantID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Agent.GetDelegatedToolInvocation(ctx, id)
	requireGrantStatus(t, err, http.StatusNotFound)
	_, err = f.Agent.DispatchDelegatedToolInvocation(ctx, lostID)
	requireGrantStatus(t, err, http.StatusNotFound)
	_, err = f.Agent.FinishDelegatedToolInvocation(ctx, lostID, out)
	requireGrantStatus(t, err, http.StatusNotFound)
	// The storage fence also applies after a handler preflight has succeeded.
	_, err = store.(state.DelegatedToolInvocationStore).ChangeDelegatedToolInvocation(ctx, f.Account.ID, lostID, *begin.Record.Delegation, "dispatch", nil)
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatal("storage accepted revoked authority after preflight", err)
	}
	// The account owner retains the durable evidence without using the delegate route.
	ownerRecord, err := f.Owner.GetToolInvocation(ctx, id)
	if err != nil || ownerRecord.Status != "completed" {
		t.Fatal("owner inspection lost retained evidence", err)
	}
	var value struct {
		StructuredContent struct {
			Value json.Number `json:"value"`
		} `json:"structuredContent"`
	}
	if json.Unmarshal(ownerRecord.Outcome.Result, &value) != nil || value.StructuredContent.Value.String() != "9007199254740993" {
		t.Fatal("result precision changed")
	}
	// Browser MCP-login keys use this same narrow native contract without the
	// headless agent scope. A separate grant never inherits another key's result.
	browserGrant := g
	browserGrant.KeyID = f.OtherKey.ID
	browserGrantID, browserInvocationID := uuid.NewString(), uuid.NewString()
	if _, err = f.Owner.PutToolGrant(ctx, browserGrantID, browserGrant); err != nil {
		t.Fatal(err)
	}
	_, err = f.Other.GetDelegatedToolInvocation(ctx, id)
	requireGrantStatus(t, err, http.StatusNotFound)
	browserIntent := in
	browserIntent.GrantID = browserGrantID
	if _, err = f.Other.BeginDelegatedToolInvocation(ctx, browserInvocationID, browserIntent); err != nil {
		t.Fatal("MCP-login creation requires more than apps-read", err)
	}
	browserDispatch, err := f.Other.DispatchDelegatedToolInvocation(ctx, browserInvocationID)
	if err != nil || !browserDispatch.DispatchGranted {
		t.Fatal("MCP-login dispatch failed", err)
	}
	if _, err = f.Other.FinishDelegatedToolInvocation(ctx, browserInvocationID, out); err != nil {
		t.Fatal(err)
	}
	// A current key revocation also fences a separate still-active grant.
	in.GrantID = otherGrant
	if err = store.DeleteAPIKey(ctx, f.Account.ID, f.AgentKey.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Agent.BeginDelegatedToolInvocation(ctx, uuid.NewString(), in)
	requireGrantStatus(t, err, http.StatusNotFound)
}
