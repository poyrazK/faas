//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/state"
)

// functionHooksReply is what the snapshot-hooks fixture answers.
type functionHooksReply struct {
	Init            bool   `json:"init"`
	BeforeSnapshots int    `json:"before_snapshots"`
	Restores        int    `json:"restores"`
	Instance        string `json:"instance"`
}

// ADR-20261110190000 slice 2: a Node function's in-code hooks run around a
// real snapshot. init finishes before the capture, beforeSnapshot runs once
// before it, and afterRestore runs in each instance restored from that one
// snapshot before it serves, giving each its own identity.
func TestFunctionSnapshotHooksMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	runner := os.Getenv("FAAS_E2E_FUNCTION_RUNNER_NODE22")
	if info, err := os.Stat(runner); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Skip("FAAS_E2E_FUNCTION_RUNNER_NODE22 must name a runner built from this tree")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	h := e2etest.Start(t, pool, e2etest.All, append(secretSealingEnv(t), "FAAS_FUNCTION_RUNNER_NODE22="+runner)...)
	key := h.SeedAccount(t.Context(), api.PlanPro)
	slug := "fn-hooks-" + randHexSuffix()
	falsy := false
	// The lifecycle a deploy sets for a handler declaring beforeSnapshot and
	// afterRestore (gregalemanifest.FunctionSpec.HookLifecycleUpdate).
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "function", Runtime: "node22", RequireAuthn: &falsy, FunctionConcurrency: 1,
		BeforeCheckpoint: &api.BeforeCheckpointHook{Path: functionlifetime.FunctionBeforeSnapshotPath},
		AfterRestore:     &api.AfterRestoreHook{Path: functionlifetime.FunctionAfterRestorePath},
	}); got != http.StatusCreated {
		t.Fatalf("create function: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, slug)
	store := state.NewPgStore(pool)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
		if _, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/"+slug, nil); status >= 300 {
			t.Errorf("retire function: status=%d", status)
		}
	})
	body, status := postBinaryFunctionDeployment(t, h, key, slug, buildTarGz(t, map[string]string{
		"handler.js": functionSnapshotHooksHandler, "package.json": `{"type":"module"}`,
	}), "node22", "handler.handler")
	if status != http.StatusAccepted {
		t.Fatalf("deploy: status=%d body=%s", status, body)
	}
	depID, _ := parseQueuedDeployment(t, body)
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatal(err)
	}
	if _, err := e2etest.WaitForAppParked(ctx, t, pool, appID, 90*time.Second); err != nil {
		t.Fatalf("prime did not park: %v", err)
	}
	waitAfterRestoreSnapshot(t, store, depID, "")

	url := gatewayAppURL(h, slug)
	host := slug + ".apps.test.example"
	client := h.HTTPClient()
	get := func() (functionHooksReply, string) {
		t.Helper()
		raw, wakeID, status := doGetWithHostCapturingWakeID(t, client, url, host, 30*time.Second)
		var reply functionHooksReply
		if status != http.StatusOK || json.Unmarshal(raw, &reply) != nil {
			t.Fatalf("GET: status=%d body=%q", status, raw)
		}
		return reply, wakeID
	}
	first, wakeID := get()
	if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, "restore", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if !first.Init || first.BeforeSnapshots != 1 || first.Restores != 1 || first.Instance == "" {
		t.Fatalf("restored worker = %+v, want init done, one beforeSnapshot before capture and one afterRestore", first)
	}

	// A second instance restored from the same snapshot runs afterRestore too
	// and gets its own identity, not the first one's.
	two := 2
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{MinInstances: &two}); status != http.StatusOK {
		t.Fatalf("min_instances=2: status=%d", status)
	}
	seen := map[string]functionHooksReply{first.Instance: first}
	deadline := time.Now().Add(2 * time.Minute)
	for len(seen) < 2 && time.Now().Before(deadline) {
		reply, _ := get()
		if reply.Restores != 1 || reply.BeforeSnapshots != 1 || !reply.Init {
			t.Fatalf("worker = %+v, want every instance restored with its hooks run once", reply)
		}
		seen[reply.Instance] = reply
		time.Sleep(500 * time.Millisecond)
	}
	if len(seen) < 2 {
		t.Fatalf("saw instances %v; want two restored instances with distinct identities", seen)
	}
}

// functionSnapshotHooksHandler counts its hooks; afterRestore draws a fresh
// identity so two restores of one snapshot can be told apart.
const functionSnapshotHooksHandler = `import { randomUUID } from "node:crypto";
let initDone = false;
let beforeSnapshots = 0;
let restores = 0;
let instance = "";
export default {
  async init() { await new Promise((r) => setTimeout(r, 200)); initDone = true; },
  beforeSnapshot() { beforeSnapshots++; },
  afterRestore() { restores++; instance = randomUUID(); },
  async fetch() {
    return Response.json({ init: initDone, before_snapshots: beforeSnapshots, restores, instance });
  },
};
`
