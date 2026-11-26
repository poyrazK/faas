package apptaskproto

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

func TestDataAPIInspectionRejectsLegacyProtocolBeforeDispatch(t *testing.T) {
	for _, version := range []uint16{Version, 4, 5, DataAPIInspectionVersion} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			host, guest := net.Pipe()
			defer host.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			called := make(chan bool, 1)
			done := make(chan error, 1)
			go func() {
				defer guest.Close()
				done <- Serve(ctx, guest, func(_ context.Context, _ Request, _, _ *OutputWriter) (Result, error) {
					called <- true
					code := 0
					return Result{Status: StatusSucceeded, ExitCode: &code}, nil
				})
				close(called)
			}()
			req := Request{Version: version, TaskID: "task", Command: []string{dataapiinspectioncommand.Command, "notes", strings.Repeat("a", 64)}, TimeoutSeconds: 1, MaxOutputBytes: 4096}
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
			if version != DataAPIInspectionVersion && (kind != FrameError || dispatched) {
				t.Fatal("legacy command reached an executable")
			}
			if version == DataAPIInspectionVersion && (kind != FrameResult || !dispatched) {
				t.Fatal("versioned inspection was not dispatched")
			}
		})
	}
	ordinary := Request{Version: DataAPIInspectionVersion, TaskID: "task", Command: []string{"true"}, TimeoutSeconds: 1, MaxOutputBytes: 4096}
	if ordinary.Validate() == nil {
		t.Fatal("ordinary command accepted as inspection")
	}
	request := Request{Version: DataAPIInspectionVersion, TaskID: "task", Command: []string{dataapiinspectioncommand.Command, "notes", strings.Repeat("a", 64)}, TimeoutSeconds: 1, MaxOutputBytes: 4096, DataAPIInspectionCA: []byte("public CA")}
	if err := request.Validate(); err != nil {
		t.Fatalf("bounded host CA rejected: %v", err)
	}
	request.DataAPIInspectionCA = make([]byte, dataapiinspectioncommand.MaxTrustBundleBytes+1)
	if request.Validate() == nil {
		t.Fatal("accepted oversized trust bundle")
	}
	request.Version, request.Command = Version, []string{"true"}
	if request.Validate() == nil {
		t.Fatal("accepted trust bundle on ordinary task")
	}
}
