//go:build linux

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

func TestCPUProfileBridgeEpochAndCheckpoint(t *testing.T) {
	sends := 0
	b := newProfileBridge(&api.ProfilingConfig{Enabled: true}, func(_ context.Context, upload profileproto.Upload) error {
		if upload.ProcessID != "42" {
			t.Error("collector process identity lost at bridge")
		}
		sends++
		return nil
	})
	handler := b.handler()
	old := b.control.Epoch
	send := func(epoch string, quoted bool) int {
		name := "gregale{gregale_epoch=" + epoch + ",gregale_process=42}"
		if quoted {
			name = "gregale{gregale_epoch=\"" + epoch + "\",gregale_process=\"42\"}"
		}
		r := httptest.NewRequest("POST", "/ingest?name="+url.QueryEscape(name), strings.NewReader("pprof"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if send(old, false) != http.StatusNoContent || send(old, true) != http.StatusNoContent || sends != 2 {
		t.Fatal("SDK epoch encodings rejected")
	}
	b.mu.Lock()
	b.processes["42"] = time.Now()
	b.mu.Unlock()
	done := make(chan struct{})
	go func() { b.checkpoint(); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		paused := b.control.Suspended
		b.mu.Unlock()
		if paused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("checkpoint did not suspend")
		}
		time.Sleep(time.Millisecond)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/control/ack?pid=42&epoch="+old, nil))
	if w.Code != 204 {
		t.Fatal("checkpoint ack rejected")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checkpoint blocked")
	}
	guestProfiles.Lock()
	previous := guestProfiles.bridge
	guestProfiles.bridge = b
	guestProfiles.Unlock()
	defer func() { guestProfiles.Lock(); guestProfiles.bridge = previous; guestProfiles.Unlock() }()
	resumeGuestProfiles()
	if b.control.Epoch == old || b.control.Suspended || len(b.processes) != 0 {
		t.Fatal("restore retained collector state")
	}
	if send(old, false) != http.StatusConflict || sends != 2 {
		t.Fatal("pre-snapshot profile accepted")
	}
	if send(b.control.Epoch, false) != http.StatusNoContent {
		t.Fatal("restored epoch rejected")
	}
}

func TestCPUProfileEnvOptInPreservesApplicationOptions(t *testing.T) {
	env := []string{"NODE_OPTIONS=--enable-source-maps", "PYTHONPATH=/app", "FAAS_PROFILING_ENDPOINT=http://wrong"}
	out := profileEnvAtPaths(env, &api.ProfilingConfig{Enabled: true}, "/managed/node.cjs", "/managed/python", func(string) bool { return true })
	text := strings.Join(out, "\n")
	for _, want := range []string{"NODE_OPTIONS=--enable-source-maps --require=/managed/node.cjs", "PYTHONPATH=/managed/python:/app", "FAAS_PROFILING_ENDPOINT=" + api.ProfileLocalEndpoint} {
		if !strings.Contains(text, want) {
			t.Fatal("managed configuration missing", text)
		}
	}
	disabled := profileEnvAtPaths(env, nil, "", "", func(string) bool { t.Fatal("disabled configuration checked runtime files"); return true })
	if strings.Join(disabled, "\n") != strings.Join(env, "\n") {
		t.Fatal("disabled configuration changed environment")
	}
	if profileQueryTime(url.Values{"from": {"9223372036854775807"}}, "from") != 0 {
		t.Fatal("timestamp overflow accepted")
	}
}
