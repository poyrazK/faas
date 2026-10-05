package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 587
func TestPlainInvocationReplayDifferentKeysAndPruning(t *testing.T) {
	e := setup(t, api.PlanPro)
	id, _ := seedInvocation(t, e, "failed", state.InvocationAsyncInvoke)
	path := "/v1/invocations/" + id + "/replay"
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan string, 8)
	failures := make(chan string, 8)
	for n := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			headers := map[string]string{}
			if n%2 == 0 {
				headers["Idempotency-Key"] = fmt.Sprintf("operator-%d", n)
			}
			rec := e.do(t, "POST", path, nil, headers)
			var result api.AsyncInvokeResponse
			if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
				failures <- fmt.Sprintf("%d %s", rec.Code, rec.Body)
				return
			}
			results <- result.ID
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	var child string
	for result := range results {
		if child != "" && child != result {
			t.Fatalf("operators created duplicate children: %s %s", child, result)
		}
		child = result
	}
	if err := forceInvocationState(t, e, child, "completed"); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "POST", path, nil, map[string]string{"Idempotency-Key": "another-key"})
	var duplicate api.AsyncInvokeResponse
	if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &duplicate) != nil || duplicate.ID != child {
		t.Fatalf("successful child re-executed: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.store.DeleteInvocationsByIDs(context.Background(), []string{child}); err != nil {
		t.Fatal(err)
	}
	// Repeating a previously successful HTTP key must observe retention now.
	rec = e.do(t, "POST", path, nil, map[string]string{"Idempotency-Key": "another-key"})
	var problem api.Problem
	if rec.Code != 409 || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != "invocation_replay_unavailable" {
		t.Fatalf("cached/pruned child returned: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.store.DeleteInvocationsByIDs(context.Background(), []string{id}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "POST", path, nil, map[string]string{"Idempotency-Key": "another-key"}); rec.Code != 404 {
		t.Fatalf("cached response bypassed parent ownership: %d %s", rec.Code, rec.Body)
	}
}

func TestPlainInvocationReplayDuplicateSurvivesPinExpiry(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := t.Context()
	appID := mustSeedApp(t, e, "plain-pins")
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := e.store.UpdateApp(ctx, appID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	var old state.Deployment
	for _, image := range []string{"sha256:old", "sha256:new"} {
		dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: appID, ImageDigest: image})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		if old.ID == "" {
			old = dep
		}
	}
	root, err := e.store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: e.acct.ID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now(), Headers: json.RawMessage(`{"X-Gregale-Revision":"` + old.ID + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := forceInvocationState(t, e, root.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	path := "/v1/invocations/" + root.ID + "/replay"
	first := e.do(t, "POST", path, nil, nil)
	var result api.AsyncInvokeResponse
	if first.Code != 202 || json.Unmarshal(first.Body.Bytes(), &result) != nil {
		t.Fatalf("initial replay: %d %s", first.Code, first.Body)
	}
	manifest.RevisionPinTTLSeconds = 0
	if _, err := e.store.UpdateApp(ctx, appID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.ResolveInvocationVersion(ctx, e.store, root); err == nil {
		t.Fatal("fixture pin did not expire")
	}
	duplicate := e.do(t, "POST", path, nil, nil)
	var repeated api.AsyncInvokeResponse
	if duplicate.Code != 202 || json.Unmarshal(duplicate.Body.Bytes(), &repeated) != nil || repeated.ID != result.ID {
		t.Fatalf("expired pin lost durable response: %d %s", duplicate.Code, duplicate.Body)
	}
	if err := forceInvocationState(t, e, result.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "POST", "/v1/invocations/"+result.ID+"/replay", nil, nil); rec.Code != http.StatusGone {
		t.Fatalf("expired pin admitted fresh recovery: %d %s", rec.Code, rec.Body)
	}
}
