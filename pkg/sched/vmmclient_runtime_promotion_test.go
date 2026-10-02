// adr: 431 — promotion clients bind fresh authority to the historical paused receipt.

package sched_test

import (
	"context"
	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"strings"
	"testing"
	"time"
)

type promotionWireServer struct {
	*admittedWireServer
	promotions      int
	mutatePromotion func(*vmmdpb.PromoteAdmittedRuntimeResponse)
}

func (s *promotionWireServer) PromoteAdmittedRuntime(_ context.Context, req *vmmdpb.PromoteAdmittedRuntimeRequest) (*vmmdpb.PromoteAdmittedRuntimeResponse, error) {
	s.promotions++
	p, _ := runtimeadmission.PromotionFromProto(req)
	r := p.Parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	resp := &vmmdpb.PromoteAdmittedRuntimeResponse{Receipt: r.ToProto()}
	if s.mutatePromotion != nil {
		s.mutatePromotion(resp)
	}
	return resp, nil
}

func TestVMMClientPromotionExactReceiptAndInvalidAcknowledgmentCleanup(t *testing.T) {
	for _, kind := range []string{"valid", "missing", "lease", "hash", "paused", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			_, b, _ := preparedWireFixture(t, true)
			parent := runtimeadmission.Receipt{Binding: b, NativeInputHash: strings.Repeat("d", 64), Netns: "native-wire", HostIP: "10.100.0.8", LeaseUID: 20008, Method: vmmdpb.WakeMethod_WAKE_RESTORE, Paused: true, CompletedAtUnixNano: time.Now().UnixNano()}
			p := runtimeadmission.Promotion{Binding: b, Parent: parent}
			p.Binding.Token = uuid.NewString()
			p.Binding.PayloadHash, _ = runtimeadmission.HashPromotionPayload(p.ToProto())
			s := &promotionWireServer{admittedWireServer: &admittedWireServer{}}
			s.mutatePromotion = func(resp *vmmdpb.PromoteAdmittedRuntimeResponse) {
				switch kind {
				case "missing":
					resp.Receipt = nil
				case "lease":
					resp.Receipt.LeaseUid++
				case "hash":
					resp.Receipt.NativeInputHash = strings.Repeat("e", 64)
				case "paused":
					resp.Receipt.Paused = true
				case "unknown":
					resp.Receipt.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
				}
			}
			client := newPolicyWireClient(t, s)
			r, err := client.PromoteAdmittedRuntime(t.Context(), p.ToProto())
			if kind == "valid" {
				if err != nil || p.CheckReceipt(r, time.Now()) != nil || len(s.destroyed) != 0 {
					t.Fatalf("valid promotion: %+v %v", r, err)
				}
			} else if err == nil || r.Binding.Token != "" || len(s.destroyed) != 1 || s.destroyed[0] != b.InstanceID {
				t.Fatalf("invalid ack leaked: %+v %v %v", r, err, s.destroyed)
			}
		})
	}
}
