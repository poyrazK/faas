// adr: 1330

package vmmdgrpc

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestJobInteractiveFromProto(t *testing.T) {
	digest := sha256.Sum256([]byte("token"))
	request := func(mutate func(*vmmdpb.JobColdBootRequest)) *vmmdpb.JobColdBootRequest {
		req := &vmmdpb.JobColdBootRequest{TaskTimeoutSec: 30,
			Interactive: &vmmdpb.JobInteractiveAttach{Tty: true, AttachTokenSha256: digest[:], AttachTimeoutSeconds: 5}}
		if mutate != nil {
			mutate(req)
		}
		return req
	}
	for _, tc := range []struct {
		name    string
		req     *vmmdpb.JobColdBootRequest
		want    *fcvm.JobInteractive
		invalid bool
	}{
		{name: "ordinary job", req: request(func(r *vmmdpb.JobColdBootRequest) { r.Interactive = nil })},
		{name: "explicit window", req: request(nil), want: &fcvm.JobInteractive{TTY: true, AttachTimeoutSec: 5}},
		{name: "default window", req: request(func(r *vmmdpb.JobColdBootRequest) {
			r.Interactive.AttachTimeoutSeconds, r.Interactive.Tty = 0, false
		}), want: &fcvm.JobInteractive{AttachTimeoutSec: 60}},
		{name: "window capped", req: request(func(r *vmmdpb.JobColdBootRequest) { r.Interactive.AttachTimeoutSeconds = 3600 }),
			want: &fcvm.JobInteractive{TTY: true, AttachTimeoutSec: 300}},
		{name: "session limit", req: request(func(r *vmmdpb.JobColdBootRequest) { r.TaskTimeoutSec = 3600 }),
			want: &fcvm.JobInteractive{TTY: true, AttachTimeoutSec: 5}},
		{name: "short digest", req: request(func(r *vmmdpb.JobColdBootRequest) { r.Interactive.AttachTokenSha256 = []byte("x") }), invalid: true},
		{name: "no digest", req: request(func(r *vmmdpb.JobColdBootRequest) { r.Interactive.AttachTokenSha256 = nil }), invalid: true},
		{name: "negative window", req: request(func(r *vmmdpb.JobColdBootRequest) { r.Interactive.AttachTimeoutSeconds = -1 }), invalid: true},
		{name: "timeout past session limit", req: request(func(r *vmmdpb.JobColdBootRequest) { r.TaskTimeoutSec = 3601 }), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, problem := jobInteractiveFromProto(tc.req)
			if tc.invalid {
				if problem == nil || problem.Status != 3 { // codes.InvalidArgument
					t.Fatalf("problem = %+v, want InvalidArgument", problem)
				}
				return
			}
			if problem != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, %+v; want %+v", got, problem, tc.want)
			}
		})
	}
}

func TestJobSessionRequestShape(t *testing.T) {
	start := &vmmdpb.AttachAppTaskStart{Rows: 40, Cols: 5000}
	tty := jobSessionRequest("job-1", &fcvm.JobInteractive{TTY: true, AttachTimeoutSec: 60}, start, 120)
	if err := tty.Validate(); err != nil {
		t.Fatalf("tty request does not validate: %v", err)
	}
	want := apptaskproto.Request{Version: apptaskproto.InteractiveVersion, TaskID: "job-1", Command: []string{"job"},
		TimeoutSeconds: 120, Interactive: true, TTY: true, Rows: 40, Cols: apptaskproto.MaxTerminalCols}
	if !reflect.DeepEqual(tty, want) {
		t.Fatalf("tty request = %+v, want %+v", tty, want)
	}
	pipes := jobSessionRequest("job-1", &fcvm.JobInteractive{AttachTimeoutSec: 60}, start, 3600)
	if err := pipes.Validate(); err != nil || pipes.TTY || pipes.Rows != 0 || pipes.Cols != 0 || pipes.TimeoutSeconds != 3600 {
		t.Fatalf("pipe request = %+v, %v", pipes, err)
	}
	defaults := jobSessionRequest("job-1", &fcvm.JobInteractive{TTY: true, AttachTimeoutSec: 60}, &vmmdpb.AttachAppTaskStart{}, 30)
	if defaults.Rows != defaultAppTaskTerminalRows || defaults.Cols != defaultAppTaskTerminalCols {
		t.Fatalf("default terminal = %dx%d", defaults.Rows, defaults.Cols)
	}
	// The session command is a validation placeholder; it must not mutate.
	tty.Command[0] = "mutated"
	if jobSessionCommand[0] != "job" {
		t.Fatal("jobSessionRequest shares jobSessionCommand's backing array")
	}
}

func TestJobSessionTerminalMapsMissingVM(t *testing.T) {
	terminal := jobSessionTerminal("job-1", apptaskproto.Result{}, errors.Join(errors.New("dial"), fcvm.ErrJobSessionInstanceNotFound), false)
	if terminal.GetStatus() != string(apptaskproto.StatusFailed) || terminal.GetFailureCode() != jobSessionEndedCode {
		t.Fatalf("terminal = %+v", terminal)
	}
	gone := jobSessionTerminal("job-1", apptaskproto.Result{}, errors.New("closed"), true)
	if gone.GetFailureCode() != appTaskClientDisconnectedCode {
		t.Fatalf("client-gone terminal = %+v", gone)
	}
}

func TestServeInteractiveJobExpiresUnclaimedRendezvous(t *testing.T) {
	old := jobInteractiveRendezvousSlack
	jobInteractiveRendezvousSlack = -990 * time.Millisecond // 1 s window - 990 ms = 10 ms
	t.Cleanup(func() { jobInteractiveRendezvousSlack = old })

	s := &Server{}
	digest := sha256.Sum256([]byte("token"))
	entry, err := s.appTaskAttach.register("job-1", digest[:])
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// jobVMM is never reached when nobody claims the rendezvous.
		s.serveInteractiveJob(context.Background(), nil, "job-1", entry, &fcvm.JobInteractive{AttachTimeoutSec: 1}, 30)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("unclaimed rendezvous did not expire")
	}
	s.appTaskAttach.mu.Lock()
	defer s.appTaskAttach.mu.Unlock()
	if _, pending := s.appTaskAttach.pending["job-1"]; pending {
		t.Fatal("expired rendezvous is still registered")
	}
}
