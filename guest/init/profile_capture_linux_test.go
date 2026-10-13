//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

func captureRequest(kinds ...string) profileproto.CaptureRequest {
	return profileproto.CaptureRequest{CaptureID: strings.Repeat("ab", 16), Kinds: kinds, DurationMillis: 1500}
}

func pollControl(t *testing.T, h http.Handler, pid string) profileControl {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/control?pid="+pid, nil))
	var c profileControl
	if err := json.NewDecoder(w.Body).Decode(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func ingest(h http.Handler, epoch, pid, kind, body string) int {
	q := url.Values{"name": {"gregale{gregale_epoch=" + epoch + ",gregale_process=" + pid + "}"}}
	if kind != "" {
		q.Set("kind", kind)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/ingest?"+q.Encode(), strings.NewReader(body)))
	return w.Code
}

func TestDormantBridgeCapturesWithoutForwarding(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error {
		t.Error("dormant bridge forwarded a capture profile")
		return nil
	})
	h := b.handler()
	if c := pollControl(t, h, "7"); c.Enabled || c.Capture {
		t.Fatalf("dormant bridge enabled collectors: %+v", c)
	}
	if ingest(h, b.control.Epoch, "7", "", "cpu") != http.StatusServiceUnavailable {
		t.Fatal("dormant bridge accepted an unrequested profile")
	}
	baseEpoch := b.control.Epoch
	sleeps := 0
	result, ack := b.runCapture(captureRequest("cpu", "heap"), func(d time.Duration) {
		sleeps++
		c := pollControl(t, h, "7")
		switch sleeps {
		case 1:
			if d != 1500*time.Millisecond || !c.Enabled || !c.Capture || c.WindowSeconds != 2 || len(c.Kinds) != 2 || c.Epoch == baseEpoch {
				t.Fatalf("capture did not arm collectors: %+v", c)
			}
			if ingest(h, c.Epoch, "7", "", "cpu-profile") != http.StatusNoContent || ingest(h, c.Epoch, "7", "heap", "heap-profile") != http.StatusNoContent {
				t.Fatal("capture upload rejected")
			}
			if ingest(h, c.Epoch, "7", "wall", "x") != http.StatusBadRequest {
				t.Fatal("unknown kind accepted")
			}
		case 2:
			if c.Enabled || c.Epoch == baseEpoch {
				t.Fatalf("window end did not stop collectors in the capture epoch: %+v", c)
			}
			if ingest(h, c.Epoch, "8", "", "late-cpu") != http.StatusNoContent {
				t.Fatal("final flush rejected")
			}
		}
	})
	if ack != profileproto.CaptureAckOK || sleeps != 2 {
		t.Fatalf("ack=%d sleeps=%d", ack, sleeps)
	}
	if len(result.Profiles) != 3 || result.Processes != 1 || result.Reason != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Profiles[1].Kind != "heap" || string(result.Profiles[1].Profile) != "heap-profile" || result.Profiles[0].ProcessID != "7" {
		t.Fatalf("captured profile identity lost: %+v", result.Profiles)
	}
	if c := pollControl(t, h, "7"); c.Enabled || c.Capture || c.Epoch == baseEpoch || b.capture != nil {
		t.Fatalf("capture end did not restore a fresh dormant epoch: %+v", c)
	}
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestContinuousBridgeForwardsCaptureCPUOnly(t *testing.T) {
	var forwarded []profileproto.Upload
	b := newProfileBridge(&api.ProfilingConfig{Enabled: true}, func(_ context.Context, u profileproto.Upload) error {
		forwarded = append(forwarded, u)
		return nil
	})
	h := b.handler()
	result, ack := b.runCapture(captureRequest("heap"), func(time.Duration) {
		c := pollControl(t, h, "9")
		if c.Enabled {
			_ = ingest(h, c.Epoch, "9", "", "cpu")
			_ = ingest(h, c.Epoch, "9", "heap", "heap")
		}
	})
	if ack != profileproto.CaptureAckOK || len(result.Profiles) != 1 || result.Profiles[0].Kind != "heap" {
		t.Fatalf("capture kept unrequested kinds: %+v", result)
	}
	if len(forwarded) != 1 || forwarded[0].Kind != "" {
		t.Fatalf("continuous CPU not forwarded exactly once: %+v", forwarded)
	}
	if c := pollControl(t, h, "9"); !c.Enabled || c.Capture || len(c.Kinds) != 1 || c.Kinds[0] != "cpu" {
		t.Fatalf("continuous configuration not restored: %+v", c)
	}
}

func TestCaptureRefusalsAndBounds(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	b.capture = &profileCapture{}
	if _, ack := b.runCapture(captureRequest("cpu"), func(time.Duration) {}); ack != profileproto.CaptureAckBusy {
		t.Fatal("concurrent capture admitted")
	}
	b.capture = nil
	b.control.Suspended = true
	if _, ack := b.runCapture(captureRequest("cpu"), func(time.Duration) {}); ack != profileproto.CaptureAckSuspended {
		t.Fatal("suspended bridge armed")
	}
	b.control.Suspended = false
	h := b.handler()
	result, _ := b.runCapture(captureRequest("cpu"), func(time.Duration) {
		c := pollControl(t, h, "3")
		if !c.Enabled {
			return
		}
		big := strings.Repeat("x", api.ProfileMaxCompressedBytes)
		for i := 0; i < profileproto.CaptureMaxProfiles+2; i++ {
			_ = ingest(h, c.Epoch, "3", "", big)
		}
	})
	if err := result.Validate(); err != nil || result.Dropped == 0 {
		t.Fatalf("capture reply not bounded: dropped=%d err=%v", result.Dropped, err)
	}
	empty, _ := b.runCapture(captureRequest("cpu"), func(time.Duration) {})
	if empty.Reason == "" || empty.Processes != 0 {
		t.Fatal("uninstrumented capture did not explain the empty result")
	}
}

func TestCaptureCheckpointAborts(t *testing.T) {
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	guestProfiles.Lock()
	previous := guestProfiles.bridge
	guestProfiles.bridge = b
	guestProfiles.Unlock()
	defer func() { guestProfiles.Lock(); guestProfiles.bridge = previous; guestProfiles.Unlock() }()
	start := time.Now()
	if !pauseGuestProfiles() || time.Since(start) > 100*time.Millisecond {
		t.Fatal("dormant checkpoint waited for collectors")
	}
	resumeGuestProfiles()
	result, _ := b.runCapture(captureRequest("cpu"), func(time.Duration) {
		if b.capture != nil {
			b.mu.Lock()
			b.capture.aborted = "instance checkpointed during capture"
			b.control.Suspended = true
			b.mu.Unlock()
		}
	})
	if result.Reason != "instance checkpointed during capture" {
		t.Fatalf("abort not reported: %+v", result)
	}
	resumeGuestProfiles()
	if c := b.control; c.Suspended || c.Enabled || b.capture != nil {
		t.Fatalf("resume did not restore dormant state: %+v", c)
	}
}

func TestProfileCaptureReplyFraming(t *testing.T) {
	header := func(n int) []byte {
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], uint32(n))
		return h[:]
	}
	if r := profileCaptureReply(bytes.NewReader(nil), header(0), nil, nil); r[0] != profileproto.CaptureAckInvalid {
		t.Fatal("empty request accepted")
	}
	if r := profileCaptureReply(bytes.NewReader([]byte("{}")), header(2), nil, nil); r[0] != profileproto.CaptureAckInvalid {
		t.Fatal("invalid request accepted")
	}
	body, _ := json.Marshal(captureRequest("cpu"))
	if r := profileCaptureReply(bytes.NewReader(body), header(len(body)), nil, nil); len(r) != 1 || r[0] != profileproto.CaptureAckUnsupported {
		t.Fatal("missing bridge not reported as unsupported")
	}
	b := newProfileBridge(nil, func(context.Context, profileproto.Upload) error { return nil })
	r := profileCaptureReply(bytes.NewReader(body), header(len(body)), b, func(time.Duration) {})
	if r[0] != profileproto.CaptureAckOK || int(binary.BigEndian.Uint32(r[1:5])) != len(r)-5 {
		t.Fatal("reply framing broken")
	}
	var result profileproto.CaptureResult
	if err := json.Unmarshal(r[5:], &result); err != nil || result.Profiles == nil {
		t.Fatalf("reply body: %v %+v", err, result)
	}
}

func TestOnDemandProfileEnvStampsDormantCollectors(t *testing.T) {
	out := profileEnvAtPaths([]string{"A=1"}, nil, true, "/managed/node.cjs", "/managed/python", func(string) bool { return true })
	text := strings.Join(out, "\n")
	for _, want := range []string{"FAAS_PROFILING_ENABLED=1", "--require=/managed/node.cjs", "PYTHONPATH=/managed/python:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("on-demand env missing %q: %s", want, text)
		}
	}
}
