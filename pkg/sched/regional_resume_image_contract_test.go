package sched

// adr: 791
import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"google.golang.org/grpc"
)

type checkedRegionalResumeRPC struct {
	*regionalResumeRPC
	support  *vmmdpb.PingResponse
	destroys int
}

func (c *checkedRegionalResumeRPC) Ping(context.Context, *vmmdpb.PingRequest, ...grpc.CallOption) (*vmmdpb.PingResponse, error) {
	return c.support, nil
}

func (c *checkedRegionalResumeRPC) Destroy(context.Context, *vmmdpb.DestroyRequest, ...grpc.CallOption) (*vmmdpb.DestroyResponse, error) {
	c.destroys++
	return &vmmdpb.DestroyResponse{}, nil
}

func TestCheckedRegionalResumeRequiresBothOriginalSeatAndImageReadiness(t *testing.T) {
	f := newRegionalWarmFixture(t)
	p := f.prepare(t)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []string{"exact", "missing-seat", "missing-image", "missing-monitor", "old-server", "transport-error"} {
		t.Run(outcome, func(t *testing.T) {
			rpc := &checkedRegionalResumeRPC{
				regionalResumeRPC: &regionalResumeRPC{response: &vmmdpb.ResumeWarmInstanceResponse{Instance: f.warm.ID, RegionalRuntime: raw, ImageHealthcheckVerified: true, SupportsImageHealthcheckMonitoring: true}},
				support:           &vmmdpb.PingResponse{SupportsImageHealthcheck: true, SupportsImageHealthcheckMonitoring: true},
			}
			lost := errors.New("resume response lost")
			switch outcome {
			case "missing-seat":
				rpc.response.RegionalRuntime = nil
			case "missing-image":
				rpc.response.ImageHealthcheckVerified = false
			case "missing-monitor":
				rpc.response.SupportsImageHealthcheckMonitoring = false
			case "old-server":
				rpc.support.SupportsImageHealthcheckMonitoring = false
			case "transport-error":
				rpc.err = lost
			}
			client := &VMMClient{cli: rpc}
			err := client.ResumeRegionalWarmInstanceWithImageHealthcheck(t.Context(), f.warm.ID, p)
			if (err == nil) != (outcome == "exact") {
				t.Fatal("partial acknowledgement accepted", err)
			}
			if outcome == "transport-error" && !errors.Is(err, lost) {
				t.Fatal("uncertain transport error lost", err)
			}
			if outcome == "old-server" {
				if rpc.request != nil {
					t.Fatal("unsupported server received a resume")
				}
			} else if rpc.request == nil || !rpc.request.ImageHealthcheckRequired || string(rpc.request.RegionalRuntime) != string(raw) {
				t.Fatal("combined resume request lost a required contract")
			}
			if rpc.destroys != 0 {
				t.Fatal("uncertain regional resident received generic destroy")
			}
		})
	}
	var client *VMMClient
	bad := p.Clone()
	bad.Seat = nil
	if err := client.ResumeRegionalWarmInstanceWithImageHealthcheck(t.Context(), f.warm.ID, bad); err == nil {
		t.Fatal("unbound checked resume reached transport")
	}
}

func TestCheckedRegionalWarmDispatchCannotUseUncheckedFallback(t *testing.T) {
	f := newRegionalWarmFixture(t)
	p := f.prepare(t)
	if err := f.engine.dispatchRegionalWarmResume(t.Context(), f.warm, p, true); !errors.Is(err, runtimequota.ErrTrust) {
		t.Fatal("unchecked regional backend accepted image contract", err)
	}
	if len(f.vmm.placements) != 0 || f.vmm.destroys != 0 || f.engine.ledger.Concurrency(f.app.ID) != 1 {
		t.Fatal("capability failure dispatched or freed retained attempt")
	}
	if err := f.store.CheckRegionalRuntimeIssuedPlacement(t.Context(), p); err != nil {
		t.Fatal("capability failure changed original seat", err)
	}
}

func TestRegionalImageAcknowledgementFailureUsesOriginalRetirement(t *testing.T) {
	f := newRegionalWarmFixture(t)
	p := f.prepare(t)
	rpc := &checkedRegionalResumeRPC{regionalResumeRPC: &regionalResumeRPC{}}
	client := &VMMClient{cli: rpc}
	err := client.confirmImageHealthcheck(t.Context(), AppSpec{ImageHealthcheckRequired: true, RegionalRuntime: &p}, f.warm.ID, &vmmdpb.WakeResponse{}, false)
	if !errors.Is(err, runtimequota.ErrTrust) || rpc.destroys != 0 {
		t.Fatal("unverified image response bypassed original retirement", err, rpc.destroys)
	}
}
