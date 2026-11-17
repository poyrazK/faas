// adr: 20261116182615 §1–§2 — a browser session carries Browser to vmmd, its
// warm snapshot is the browser shape's (captured with the prewarm flag,
// under keys of its own), and a plain preset session never restores it.
package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

func TestSandboxBrowserShape(t *testing.T) {
	runner, router, _ := snapshotFixture(t, true)
	ctx := context.Background()
	n := 0
	session := func(browser bool) sched.SandboxSpec {
		t.Helper()
		n++
		a := sandboxAdmission()
		a.SandboxID = "22222222-2222-3333-4444-00000000000" + string(rune('0'+n))
		a.Browser = browser
		if err := runner.RunSandbox(ctx, a, nil, sched.SandboxHooks{}, make(chan sched.SandboxControl)); err != nil {
			t.Fatal(err)
		}
		return router.lastSpec()
	}
	captured := func(want int) []capture {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for len(router.captures()) < want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		got := router.captures()
		if len(got) != want {
			t.Fatalf("%d captures, want %d", len(got), want)
		}
		return got
	}

	if s := session(false); s.Browser || s.Snapshot != nil {
		t.Fatalf("plain session %+v", s)
	}
	plain := captured(1)[0]
	if s := session(true); !s.Browser || s.Snapshot != nil {
		t.Fatalf("the first browser session %+v restored the plain shape's snapshot", s)
	}
	browser := captured(2)[1]
	if !browser.spec.Browser || plain.spec.Browser || browser.captureID == plain.captureID {
		t.Fatalf("captures plain %+v browser %+v; want a browser capture under keys of its own", plain, browser)
	}
	if s := session(true); s.Snapshot == nil || s.Snapshot.StorageKey != "snap/"+browser.namespace+"/captures/"+browser.captureID+"/v2/mem" {
		t.Fatalf("a browser session restored %+v, want the browser shape's snapshot", s.Snapshot)
	}
	if s := session(false); s.Snapshot == nil || s.Snapshot.StorageKey == "snap/"+browser.namespace+"/captures/"+browser.captureID+"/v2/mem" {
		t.Fatalf("a plain session restored %+v", s.Snapshot)
	}
	a := sandboxAdmission()
	a.Browser, a.ImageKey, a.StartCmd = true, build1, "serve"
	var invalid *sched.AgentRunInvalidError
	if err := runner.RunSandbox(ctx, a, nil, sched.SandboxHooks{}, make(chan sched.SandboxControl)); err == nil {
		t.Fatal("a browser session with its own commands was admitted")
	} else if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want AgentRunInvalidError", err)
	}
}
