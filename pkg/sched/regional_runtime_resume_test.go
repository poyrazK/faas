package sched

// adr: 624
import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"google.golang.org/grpc"
)

type regionalResumeRPC struct {
	vmmdpb.VmmdClient
	response *vmmdpb.ResumeWarmInstanceResponse
	err      error
	request  *vmmdpb.ResumeWarmInstanceRequest
}

func (c *regionalResumeRPC) ResumeWarmInstance(_ context.Context, req *vmmdpb.ResumeWarmInstanceRequest, _ ...grpc.CallOption) (*vmmdpb.ResumeWarmInstanceResponse, error) {
	c.request = req
	return c.response, c.err
}

func TestRegionalResumeRequiresExactTransportAcknowledgement(t *testing.T) {
	store, account, app, dep := regionalQuotaSchedulerFixture(t)
	freezeRegionalSchedulerApp(t, store, account, app)
	head, err := store.RegionalRuntimeQuotaHead(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := runtimequota.CapacityPermit{Grant: head.Grant, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.MustParse(dep.ID).String(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityWarm}
	placement := runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}
	if err := placement.Validate(); err != nil {
		t.Fatal("invalid fixture", err)
	}
	raw, err := json.Marshal(placement)
	if err != nil {
		t.Fatal(err)
	}
	for _, response := range []string{"exact", "compact-key", "wrong-key-encoding", "old-server", "wrong-instance", "wrong-seat", "transport-error"} {
		t.Run(response, func(t *testing.T) {
			rpc := &regionalResumeRPC{response: &vmmdpb.ResumeWarmInstanceResponse{Instance: p.InstanceID, RegionalRuntime: raw}}
			unavailable := errors.New("resume RPC failed after dispatch")
			instance := p.InstanceID
			switch response {
			case "compact-key":
				instance = strings.ReplaceAll(instance, "-", "")
				rpc.response.Instance = instance
			case "wrong-key-encoding":
				instance = strings.ReplaceAll(instance, "-", "")
			case "old-server":
				rpc.response.RegionalRuntime = nil
			case "wrong-instance":
				rpc.response.Instance = uuid.NewString()
			case "wrong-seat":
				changed := placement.Clone()
				changed.Seat.ID = uuid.NewString()
				rpc.response.RegionalRuntime, _ = json.Marshal(changed)
			case "transport-error":
				rpc.err = unavailable
			}
			client := &VMMClient{cli: rpc}
			err := client.ResumeRegionalWarmInstance(t.Context(), instance, placement)
			exact := response == "exact" || response == "compact-key"
			if exact && err != nil {
				t.Fatal(err)
			}
			if !exact && err == nil {
				t.Fatal("ambiguous resume acknowledgement accepted")
			}
			if response == "transport-error" && !errors.Is(err, unavailable) {
				t.Fatal("uncertain RPC error hidden", err)
			}
			decoded, err := runtimequota.DecodePlacement(rpc.request.GetRegionalRuntime())
			if err != nil || decoded.Permit != placement.Permit || *decoded.Seat != *placement.Seat || rpc.request.Instance != instance {
				t.Fatal("resume request lost immutable pins", err)
			}
		})
	}
	for _, malformed := range []string{"missing-seat", "wrong-instance", "invalid-placement"} {
		changed := placement.Clone()
		switch malformed {
		case "missing-seat":
			changed.Seat = nil
		case "wrong-instance":
			changed.Permit.InstanceID = uuid.NewString()
			changed.Seat.Permit = changed.Permit
		case "invalid-placement":
			changed = runtimequota.Placement{}
		}
		// Validation must precede transport access.
		var client *VMMClient
		if err := client.ResumeRegionalWarmInstance(t.Context(), p.InstanceID, changed); err == nil {
			t.Fatal("invalid resume reached transport", malformed)
		}
	}
	var router *VMMRouter
	if err := router.ResumeRegionalWarmInstance(t.Context(), uuid.NewString(), p.InstanceID, placement); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal("cross-node resume reached resolution", err)
	}
}
