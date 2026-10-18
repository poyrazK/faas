package apptaskproto

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

func TestContainedCommandWireRejectsLegacyEnvelopeBeforeDispatch(t *testing.T) {
	spec := rehearsalcommand.Spec{Version: 1, RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", Command: []string{"true"}}
	argv, _ := rehearsalcommand.Encode(spec)
	for _, version := range []uint16{Version, RehearsalVersion} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			host, guest := net.Pipe()
			defer func() { _ = host.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			called := make(chan bool, 1)
			done := make(chan error, 1)
			go func() {
				defer func() { _ = guest.Close() }()
				err := Serve(ctx, guest, func(_ context.Context, req Request, _, _ *OutputWriter) (Result, error) {
					called <- true
					exit := 0
					return Result{Status: StatusSucceeded, ExitCode: &exit}, nil
				})
				done <- err
				close(called)
			}()
			req := Request{Version: version, TaskID: "22222222-2222-2222-2222-222222222222", Command: argv, TimeoutSeconds: 1, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes}
			raw, _ := json.Marshal(req)
			if err := writeFrame(ctx, host, FrameRequest, raw); err != nil {
				t.Fatal(err)
			}
			kind, _, err := readFrame(ctx, host)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			dispatched := <-called
			if version == Version && (kind != FrameError || dispatched) {
				t.Fatal("legacy request dispatched the reserved executable")
			}
			if version == RehearsalVersion && (kind != FrameResult || !dispatched) {
				t.Fatal("contained request was not recognized")
			}
		})
	}
	ordinary := Request{Version: RehearsalVersion, TaskID: "task", Command: []string{"true"}, TimeoutSeconds: 1, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes}
	if ordinary.Validate() == nil {
		t.Fatal("ordinary argv accepted as contained protocol")
	}
}
