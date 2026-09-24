package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mirrorParkCall struct {
	appID, instanceID, traceID string
	ctxErr                     error
}

type mirrorParkerBackend struct {
	*mirrorTargetFakeBackend
	parks []mirrorParkCall
}

func (b *mirrorParkerBackend) ParkMirrorInstance(ctx context.Context, appID, instanceID, traceID string) error {
	b.parks = append(b.parks, mirrorParkCall{appID: appID, instanceID: instanceID, traceID: traceID, ctxErr: ctx.Err()})
	return nil
}

func TestDispatchMirrorParksAdmittedInstanceOnEveryExit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		scheduleErr error
		forwardErr  error
		wantParks   int
	}{
		{name: "success", wantParks: 1},
		{name: "forward failure", forwardErr: errors.New("forward failed"), wantParks: 1},
		{name: "admission failure", scheduleErr: errors.New("admission failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &mirrorFakeBackend{scheduleErr: tc.scheduleErr}
			backend := &mirrorParkerBackend{mirrorTargetFakeBackend: &mirrorTargetFakeBackend{
				mirrorFakeBackend: base,
				target:            Target{AppID: "app", NodeID: "node", InstanceID: "shadow", DeploymentID: "mirror-dep"},
			}}
			rt := &stubMirrorRoundTripper{cannedErr: tc.forwardErr,
				cannedResponse: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}}
			h := &Handler{backend: backend, log: slog.New(slog.NewTextHandler(io.Discard, nil)), mirrorRoundTripper: rt}
			capture := newMirrorSourceCapture()
			capture.writeHeader(200)
			capture.write([]byte("ok"))
			capture.complete()
			parent, cancel := context.WithCancel(context.Background())
			cancel() // customer disconnect must not cancel mirror cleanup
			h.dispatchMirror(parent, "source", nil, MirrorRuleRow{ID: "rule", AppID: "app", MirrorDeploymentID: "mirror-dep"},
				httptest.NewRequest(http.MethodGet, "http://example.test/", nil), nil, "request-id", capture)
			if len(backend.parks) != tc.wantParks {
				t.Fatalf("park calls=%d, want %d", len(backend.parks), tc.wantParks)
			}
			if tc.wantParks == 1 {
				got := backend.parks[0]
				if got.appID != "app" || got.instanceID != "shadow" || got.ctxErr != nil {
					t.Fatalf("park call=%+v", got)
				}
			}
		})
	}
}

type mirrorParkScheduler struct {
	Scheduler
	instanceID, reason, traceID string
}

func (s *mirrorParkScheduler) ParkInstance(_ context.Context, instanceID, reason, traceID string) error {
	s.instanceID, s.reason, s.traceID = instanceID, reason, traceID
	return nil
}

func TestPGBackendParkMirrorInstanceUsesOwningScheduler(t *testing.T) {
	scheduler := &mirrorParkScheduler{}
	backend := &PGBackend{sched: scheduler}
	if err := backend.ParkMirrorInstance(context.Background(), "app", "shadow", "trace"); err != nil {
		t.Fatal(err)
	}
	if scheduler.instanceID != "shadow" || scheduler.reason != "mirror_dispatch_complete" || scheduler.traceID != "trace" {
		t.Fatalf("park=(%q,%q,%q)", scheduler.instanceID, scheduler.reason, scheduler.traceID)
	}
}
