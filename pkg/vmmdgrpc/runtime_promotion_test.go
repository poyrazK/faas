package vmmdgrpc_test

import (
	"context"
	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func (v *admittedNativeVMM) PromoteAdmitted(_ context.Context, p runtimeadmission.Promotion) (*fcvm.Instance, runtimeadmission.Receipt, error) {
	v.promotions = append(v.promotions, p)
	r := p.Parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	l := fcvm.Lease{Instance: p.Binding.InstanceID, UID: int(r.LeaseUID), HostIP: netip.MustParseAddr(r.HostIP)}
	inst := &fcvm.Instance{Lease: l, Net: netns.NewConfig(l.Instance, r.Netns, "vh1", "vp1", l.HostIP), Method: fcvm.WakeRestore, AppID: p.Binding.AppID, AccountID: p.Binding.AccountID, DeploymentID: p.Binding.DeploymentID}
	if v.mutate != nil {
		v.mutate(inst, &r)
	}
	return inst, r, nil
}

func TestPromoteAdmittedRuntimeAuthenticatesAndChecksNativeReceipt(t *testing.T) {
	for _, kind := range []string{"valid", "wrong-peer", "unknown-input", "old-process", "bad-native-lease", "still-paused"} {
		t.Run(kind, func(t *testing.T) {
			s, v, boot := admittedRPCFixture(t)
			b, _ := runtimeadmission.BindingFromProto(boot.Binding)
			parent := runtimeadmission.Receipt{Binding: b, NativeInputHash: strings.Repeat("d", 64), Netns: "native-promotion", HostIP: "10.100.0.8", LeaseUID: 20008, Method: vmmdpb.WakeMethod_WAKE_RESTORE, Paused: true, CompletedAtUnixNano: time.Now().UnixNano()}
			p := runtimeadmission.Promotion{Binding: b, Parent: parent}
			p.Binding.Token = uuid.NewString()
			p.Binding.PayloadHash, _ = runtimeadmission.HashPromotionPayload(p.ToProto())
			req := p.ToProto()
			ctx := admittedRPCContext(t)
			switch kind {
			case "wrong-peer":
				ctx = t.Context()
			case "unknown-input":
				req.Parent.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
			case "old-process":
				req.Binding.Incarnation = uuid.NewString()
			case "bad-native-lease":
				v.mutate = func(inst *fcvm.Instance, r *runtimeadmission.Receipt) { inst.Lease.UID++ }
			case "still-paused":
				v.mutate = func(inst *fcvm.Instance, r *runtimeadmission.Receipt) { inst.Paused = true; r.Paused = true }
			}
			resp, err := s.PromoteAdmittedRuntime(ctx, req)
			if kind == "valid" {
				r, decodeErr := runtimeadmission.ReceiptFromProto(resp.GetReceipt())
				if err != nil || decodeErr != nil || p.CheckReceipt(r, time.Now()) != nil || len(v.promotions) != 1 || len(v.destroyed) != 0 {
					t.Fatalf("native receipt: %+v %v", resp, err)
				}
			} else if kind == "bad-native-lease" || kind == "still-paused" {
				if err == nil || resp != nil || len(v.destroyed) != 1 {
					t.Fatalf("malformed native success leaked: %v %v", resp, err)
				}
			} else if err == nil || resp != nil || len(v.promotions) != 0 || (kind == "wrong-peer" && status.Code(err) != codes.Unauthenticated) {
				t.Fatalf("invalid request reached native resume: %v %v", resp, err)
			}
		})
	}
}
