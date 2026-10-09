package main

// adr: 733

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/crashcapturewire"
	"github.com/onebox-faas/faas/pkg/state"
)

type crashRecvFixture struct {
	store    *state.MemStore
	acctID   string
	appID    string
	instance string
}

func newCrashRecvFixture(t *testing.T, optIn bool) crashRecvFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "crash@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "crash", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: "live"})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), 256, "", "wake-1", string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCrashSnapshotSettings(ctx, acct.ID, app.ID, optIn, time.Now()); err != nil {
		t.Fatal(err)
	}
	return crashRecvFixture{store: store, acctID: acct.ID, appID: app.ID, instance: ins.ID}
}

func (f crashRecvFixture) receiver(enabled bool) *CrashCaptureReceiver {
	r := newCrashCaptureReceiver(nil, f.store, func(instance string) (string, string, error) {
		if instance != f.instance {
			return "", "", errors.New("not live")
		}
		return f.appID, f.acctID, nil
	}, enabled)
	r.poll = 10 * time.Millisecond
	return r
}

// ask sends one request on a pipe, as guest-init would, and returns every
// response frame until the final one.
func ask(t *testing.T, r *CrashCaptureReceiver, instance string, req crashcapturewire.Request) []crashcapturewire.Response {
	t.Helper()
	guest, host := net.Pipe()
	defer func() { _ = guest.Close() }()
	go func() {
		_, _ = r.handleGuestStream(instance, host)
		_ = host.Close()
	}()
	if err := crashcapturewire.WriteFrame(guest, req); err != nil {
		t.Fatal(err)
	}
	var out []crashcapturewire.Response
	for {
		var resp crashcapturewire.Response
		if err := crashcapturewire.ReadFrame(guest, &resp); err != nil {
			t.Fatalf("read response: %v (so far %+v)", err, out)
		}
		out = append(out, resp)
		if resp.Final() {
			return out
		}
	}
}

func TestCrashCaptureReceiver_NotEnabledAndRefused(t *testing.T) {
	f := newCrashRecvFixture(t, false)
	if got := ask(t, f.receiver(false), f.instance, crashcapturewire.Request{}); got[0].Status != crashcapturewire.StatusNotEnabled {
		t.Fatalf("flag off = %+v, want not_enabled", got)
	}
	if got := ask(t, f.receiver(true), f.instance, crashcapturewire.Request{}); got[0].Status != crashcapturewire.StatusRefused {
		t.Fatalf("no opt-in = %+v, want refused", got)
	}
	if got := ask(t, f.receiver(true), "someone-else", crashcapturewire.Request{}); got[0].Status != crashcapturewire.StatusUnavailable {
		t.Fatalf("unknown instance = %+v, want unavailable", got)
	}
}

func TestCrashCaptureReceiver_WaitsForTheCapture(t *testing.T) {
	f := newCrashRecvFixture(t, true)
	r := f.receiver(true)
	// Stand in for schedd: claim and complete the capture once requested.
	go func() {
		ctx := context.Background()
		for range 200 {
			c, err := f.store.ClaimNextCrashCapture(ctx, time.Now())
			if err == nil {
				now := time.Now()
				_, _ = f.store.CompleteCrashCapture(ctx, state.CompleteCrashCaptureParams{
					ID: c.ID, StorageKey: "mem", VMStateStorageKey: "vmstate", FCVersion: "1.7.0",
					MemBytes: 1, CapturedAt: now, ExpiresAt: now.Add(time.Hour),
				})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	got := ask(t, r, f.instance, crashcapturewire.Request{Reason: "checkout panic", Route: "/orders", WaitMs: 5000})
	if len(got) != 2 || got[0].Status != crashcapturewire.StatusRequested || got[0].CaptureID == "" ||
		got[1].Status != crashcapturewire.StatusCaptured || got[1].CaptureID != got[0].CaptureID {
		t.Fatalf("responses = %+v, want requested then captured", got)
	}
	c, err := f.store.CrashCaptureForRestore(context.Background(), got[0].CaptureID)
	if err != nil || c.Trigger != state.CrashTriggerSDK || c.InstanceID != f.instance || c.Reason != "checkout panic" || c.Route != "/orders" {
		t.Fatalf("capture row = %+v, %v", c, err)
	}
}

func TestCrashCaptureReceiver_PendingWhenTheWaitRunsOut(t *testing.T) {
	f := newCrashRecvFixture(t, true)
	got := ask(t, f.receiver(true), f.instance, crashcapturewire.Request{WaitMs: 50})
	if len(got) != 2 || got[1].Status != crashcapturewire.StatusPending || got[1].CaptureID == "" {
		t.Fatalf("responses = %+v, want requested then pending", got)
	}
}

func TestCrashCaptureRequestWaitIsBounded(t *testing.T) {
	for _, tc := range []struct {
		ms   int
		want time.Duration
	}{{0, crashcapturewire.DefaultWait}, {-5, crashcapturewire.DefaultWait}, {2000, 2 * time.Second}, {600000, crashcapturewire.MaxWait}} {
		if got := (crashcapturewire.Request{WaitMs: tc.ms}).Wait(); got != tc.want {
			t.Errorf("Wait(%d) = %v, want %v", tc.ms, got, tc.want)
		}
	}
}
