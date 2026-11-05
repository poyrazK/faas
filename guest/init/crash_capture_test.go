package main

// adr: 733

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/crashcapturewire"
)

// fakeHost answers one request with frames, then optionally breaks the
// stream (as a fork restore does) instead of closing cleanly.
func fakeHost(t *testing.T, frames []crashcapturewire.Response, onBreak func()) func() (net.Conn, error) {
	t.Helper()
	return func() (net.Conn, error) {
		guest, host := net.Pipe()
		go func() {
			defer func() { _ = host.Close() }()
			var req crashcapturewire.Request
			if err := crashcapturewire.ReadFrame(host, &req); err != nil {
				return
			}
			for _, f := range frames {
				if err := crashcapturewire.WriteFrame(host, f); err != nil {
					return
				}
			}
			if onBreak != nil {
				onBreak()
			}
		}()
		return guest, nil
	}
}

func TestRequestCrashCapture_Outcomes(t *testing.T) {
	requested := crashcapturewire.Response{Status: crashcapturewire.StatusRequested, CaptureID: "c1"}
	for _, tc := range []struct {
		name   string
		frames []crashcapturewire.Response
		want   crashcapturewire.Response
		code   int
	}{
		{"captured", []crashcapturewire.Response{requested, {Status: crashcapturewire.StatusCaptured, CaptureID: "c1"}},
			crashcapturewire.Response{Status: crashcapturewire.StatusCaptured, CaptureID: "c1"}, http.StatusOK},
		{"refused", []crashcapturewire.Response{{Status: crashcapturewire.StatusRefused}},
			crashcapturewire.Response{Status: crashcapturewire.StatusRefused}, http.StatusConflict},
		{"failed", []crashcapturewire.Response{requested, {Status: crashcapturewire.StatusFailed, CaptureID: "c1", Code: "capture_failed"}},
			crashcapturewire.Response{Status: crashcapturewire.StatusFailed, CaptureID: "c1", Code: "capture_failed"}, http.StatusBadGateway},
		{"not enabled", []crashcapturewire.Response{{Status: crashcapturewire.StatusNotEnabled}},
			crashcapturewire.Response{Status: crashcapturewire.StatusNotEnabled}, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := requestCrashCapture(crashcapturewire.Request{WaitMs: 1000}, fakeHost(t, tc.frames, nil))
			if got != tc.want || crashCaptureHTTPStatus(got) != tc.code {
				t.Fatalf("got %+v (%d), want %+v (%d)", got, crashCaptureHTTPStatus(got), tc.want, tc.code)
			}
		})
	}
}

// A stream that breaks after the capture was requested is a fork only when
// a restore follows; otherwise the capture is still pending.
func TestRequestCrashCapture_BrokenStream(t *testing.T) {
	requested := []crashcapturewire.Response{{Status: crashcapturewire.StatusRequested, CaptureID: "c9"}}
	got := requestCrashCapture(crashcapturewire.Request{}, fakeHost(t, requested, func() {
		go func() {
			time.Sleep(20 * time.Millisecond)
			restoreGeneration.bump()
		}()
	}))
	if got.Status != crashcapturewire.StatusCaptured || !got.InFork || got.CaptureID != "c9" {
		t.Fatalf("after a restore = %+v, want captured in the fork", got)
	}

	start := time.Now()
	got = requestCrashCapture(crashcapturewire.Request{}, fakeHost(t, requested, nil))
	if got.Status != crashcapturewire.StatusPending || got.InFork || got.CaptureID != "c9" {
		t.Fatalf("without a restore = %+v, want pending", got)
	}
	if time.Since(start) < crashCaptureForkGrace {
		t.Fatalf("decided after %v, before the fork grace", time.Since(start))
	}

	got = requestCrashCapture(crashcapturewire.Request{}, fakeHost(t, nil, nil))
	if got.Status != crashcapturewire.StatusUnavailable {
		t.Fatalf("no answer = %+v, want unavailable", got)
	}
	if got := requestCrashCapture(crashcapturewire.Request{}, nil); got.Status != crashcapturewire.StatusUnavailable {
		t.Fatalf("no vsock = %+v, want unavailable", got)
	}
}

func TestRestoreCounterWakesWaiters(t *testing.T) {
	c := newRestoreCounter()
	gen := c.current()
	if c.changedSince(gen, 10*time.Millisecond) {
		t.Fatal("changed without a bump")
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		c.bump()
	}()
	if !c.changedSince(gen, time.Second) {
		t.Fatal("bump did not wake the waiter")
	}
	if c.current() != gen+1 {
		t.Fatalf("generation = %d, want %d", c.current(), gen+1)
	}
}

// A fork whose old stream is never reset still returns promptly once the
// restore hook runs, instead of waiting out the stream's deadline.
func TestRequestCrashCapture_RestoreWakesASilentStream(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	dial := func() (net.Conn, error) {
		guest, host := net.Pipe()
		go func() {
			defer func() { _ = host.Close() }()
			var req crashcapturewire.Request
			if crashcapturewire.ReadFrame(host, &req) != nil {
				return
			}
			_ = crashcapturewire.WriteFrame(host, crashcapturewire.Response{Status: crashcapturewire.StatusRequested, CaptureID: "c7"})
			<-release // silent: no reset, no answer
		}()
		return guest, nil
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		restoreGeneration.bump()
	}()
	start := time.Now()
	got := requestCrashCapture(crashcapturewire.Request{WaitMs: 20000}, dial)
	if got.Status != crashcapturewire.StatusCaptured || !got.InFork || got.CaptureID != "c7" {
		t.Fatalf("got %+v, want captured in the fork", got)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("returned after %v; the restore should wake it at once", took)
	}
}
