package scheddgrpc

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordingRelay struct {
	got []sched.InstanceFailureReport
	err error
}

func (r *recordingRelay) RelayInstanceFailure(_ context.Context, report sched.InstanceFailureReport) error {
	r.got = append(r.got, report)
	return r.err
}

// Issue #3359: vmmd reports a dead guest to the schedd on its own node,
// which rejected it when a peer schedd owned the app, leaving the instance
// RUNNING. The host schedd must relay reports for instances it hosts, and
// only for those.
func TestReportLivenessFailed_RelaysHostedForeignInstance(t *testing.T) {
	const self, peer = "node-host", "node-owner"
	cases := []struct {
		name      string
		hostNode  string
		relay     *recordingRelay
		wantCode  codes.Code
		wantRelay bool
	}{
		{name: "hosted here, owned by peer", hostNode: self, relay: &recordingRelay{}, wantCode: codes.OK, wantRelay: true},
		{name: "hosted on another node", hostNode: "node-other", relay: &recordingRelay{}, wantCode: codes.FailedPrecondition},
		{name: "no relay wired", hostNode: self, wantCode: codes.FailedPrecondition},
		{name: "relay publish fails", hostNode: self, relay: &recordingRelay{err: errors.New("pg down")}, wantCode: codes.Unavailable, wantRelay: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := &fakeResolver{
				apps:  map[string]state.App{"app-1": {ID: "app-1", NodeID: peer}},
				insts: map[string]state.Instance{"ins-1": {ID: "ins-1", AppID: "app-1", NodeID: tc.hostNode}},
			}
			srv := New(nil, nil, nil).WithOwner(OwnerNodeID(self), res)
			if tc.relay != nil {
				srv.WithForeignReportRelay(tc.relay)
			}

			ack, err := srv.ReportLivenessFailed(context.Background(), &scheddpb.LivenessFailedReport{
				InstanceId: "ins-1", Reason: "liveness_process_exited",
			})

			if got := status.Code(err); got != tc.wantCode {
				t.Fatalf("code = %v (err %v), want %v", got, err, tc.wantCode)
			}
			if tc.wantCode == codes.OK && !ack.GetOk() {
				t.Fatalf("ack.Ok = false, want true")
			}
			var relayed []sched.InstanceFailureReport
			if tc.relay != nil {
				relayed = tc.relay.got
			}
			if !tc.wantRelay {
				if len(relayed) != 0 {
					t.Fatalf("relayed %d report(s), want none", len(relayed))
				}
				return
			}
			want := sched.InstanceFailureReport{
				InstanceID: "ins-1", AppID: "app-1", Kind: sched.InstanceFailureLiveness, Reason: "liveness_process_exited",
			}
			if len(relayed) != 1 || relayed[0] != want {
				t.Fatalf("relayed = %+v, want [%+v]", relayed, want)
			}
		})
	}
}

func TestReportWorkloadOOM_RelaysHostedForeignInstance(t *testing.T) {
	res := &fakeResolver{
		apps:  map[string]state.App{"app-1": {ID: "app-1", NodeID: "node-owner"}},
		insts: map[string]state.Instance{"ins-1": {ID: "ins-1", AppID: "app-1", NodeID: "node-host"}},
	}
	relay := &recordingRelay{}
	srv := New(nil, nil, nil).WithOwner("node-host", res).WithForeignReportRelay(relay)

	ack, err := srv.ReportWorkloadOOM(context.Background(), &scheddpb.ReportWorkloadOOMRequest{
		InstanceId: "ins-1", PeakMb: 300, PlanMb: 256,
	})
	if err != nil || !ack.GetOk() {
		t.Fatalf("ReportWorkloadOOM = (%v, %v), want ok", ack, err)
	}
	want := sched.InstanceFailureReport{
		InstanceID: "ins-1", AppID: "app-1", Kind: sched.InstanceFailureWorkloadOOM, PeakMB: 300, PlanMB: 256,
	}
	if len(relay.got) != 1 || relay.got[0] != want {
		t.Fatalf("relayed = %+v, want [%+v]", relay.got, want)
	}
}
