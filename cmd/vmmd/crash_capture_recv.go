package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/crashcapturewire"
	"github.com/onebox-faas/faas/pkg/state"
)

// crashCaptureStore is the slice of state vmmd needs for the ADR-733 SDK
// trigger: write the request, then read it back until schedd finishes it.
type crashCaptureStore interface {
	RequestSDKCrashCapture(ctx context.Context, appID, instanceID, route, reason string, cooldown time.Duration, now time.Time) (state.CrashCapture, error)
	CrashCaptureForRestore(ctx context.Context, id string) (state.CrashCapture, error)
}

// instanceIdentity resolves the app that owns a live instance.
type instanceIdentity func(instance string) (appID, accountID string, err error)

// CrashCaptureReceiver serves the SDK trigger: an app asks for a crash
// capture of its own instance from inside its error handler. The listener
// that accepted the stream names the instance, so a guest can only ever
// capture itself.
type CrashCaptureReceiver struct {
	log      *slog.Logger
	store    crashCaptureStore
	identity instanceIdentity
	enabled  bool
	now      func() time.Time
	poll     time.Duration
}

func newCrashCaptureReceiver(log *slog.Logger, store crashCaptureStore, identity instanceIdentity, enabled bool) *CrashCaptureReceiver {
	if log == nil {
		log = slog.Default()
	}
	return &CrashCaptureReceiver{log: log, store: store, identity: identity, enabled: enabled, now: time.Now, poll: 250 * time.Millisecond}
}

func (r *CrashCaptureReceiver) Close() {}

func (r *CrashCaptureReceiver) handleGuestStream(instance string, conn net.Conn) (string, error) {
	if err := conn.SetDeadline(r.now().Add(5 * time.Second)); err != nil {
		return "read", err
	}
	var req crashcapturewire.Request
	if err := crashcapturewire.ReadFrame(conn, &req); err != nil {
		return "protocol", err
	}
	if !r.enabled || r.store == nil || r.identity == nil {
		return writeCrashResponse(conn, crashcapturewire.Response{Status: crashcapturewire.StatusNotEnabled})
	}
	appID, _, err := r.identity(instance)
	if err != nil || appID == "" {
		return writeCrashResponse(conn, crashcapturewire.Response{Status: crashcapturewire.StatusUnavailable, Code: "instance_not_found"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), req.Wait()+5*time.Second)
	defer cancel()
	capture, err := r.store.RequestSDKCrashCapture(ctx, appID, instance, req.Route, req.Reason, api.CrashCaptureCooldown, r.now().UTC())
	if errors.Is(err, state.ErrCrashCaptureRefused) {
		// Normal (no opt-in, one in flight, cooldown), but the only trace
		// of why an app's own request was turned away.
		r.log.Info("vmmd: sdk crash capture refused", "instance", instance, "app", appID)
		return writeCrashResponse(conn, crashcapturewire.Response{Status: crashcapturewire.StatusRefused})
	}
	if err != nil {
		r.log.Warn("vmmd: sdk crash capture request", "instance", instance, "err", err)
		return writeCrashResponse(conn, crashcapturewire.Response{Status: crashcapturewire.StatusUnavailable, Code: "store_error"})
	}
	r.log.Info("vmmd: sdk crash capture requested", "instance", instance, "app", appID, "capture", capture.ID)
	if err := conn.SetDeadline(r.now().Add(req.Wait() + 5*time.Second)); err != nil {
		return "write", err
	}
	if kind, err := writeCrashResponse(conn, crashcapturewire.Response{Status: crashcapturewire.StatusRequested, CaptureID: capture.ID}); err != nil {
		return kind, err
	}
	return writeCrashResponse(conn, r.waitForCapture(ctx, capture.ID, req.Wait()))
}

// waitForCapture polls the row until schedd finishes it or wait runs out.
// The instance is paused and resumed in between; this stream survives that.
func (r *CrashCaptureReceiver) waitForCapture(ctx context.Context, id string, wait time.Duration) crashcapturewire.Response {
	deadline := r.now().Add(wait)
	for {
		c, err := r.store.CrashCaptureForRestore(ctx, id)
		if err == nil {
			switch c.Status {
			case state.CrashCaptureReady, state.CrashCaptureExpired:
				return crashcapturewire.Response{Status: crashcapturewire.StatusCaptured, CaptureID: id}
			case state.CrashCaptureFailed:
				code := ""
				if c.FailureCode != nil {
					code = *c.FailureCode
				}
				return crashcapturewire.Response{Status: crashcapturewire.StatusFailed, CaptureID: id, Code: code}
			}
		}
		if !r.now().Before(deadline) {
			return crashcapturewire.Response{Status: crashcapturewire.StatusPending, CaptureID: id}
		}
		select {
		case <-ctx.Done():
			return crashcapturewire.Response{Status: crashcapturewire.StatusPending, CaptureID: id}
		case <-time.After(r.poll):
		}
	}
}

func writeCrashResponse(conn net.Conn, resp crashcapturewire.Response) (string, error) {
	if err := crashcapturewire.WriteFrame(conn, resp); err != nil {
		return "write", err
	}
	return "", nil
}
