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

// adr: 595 Complete wire lineage with explicit simulated native acknowledgments.
func TestPromoteMeasuredSnapshotRuntimeRetainsAndChecksResumeReceipt(t *testing.T) {
	for _, fault := range []string{"complete", "missing", "command", "grant", "parent", "process", "mapping", "unknown-parent", "unsupported"} {
		t.Run(fault, func(t *testing.T) {
			s, v, boot := admittedRPCFixture(t)
			rpcSnapshotRestoreEnvelope(t, boot)
			v.identity.ProtocolVersion, v.identity.SnapshotRestoreVersion = runtimeadmission.ArtifactProtocolVersion, runtimeadmission.SnapshotRestoreVersion
			boot.GetRestore().KeepPaused = true
			rehashAdmittedRequest(t, boot)
			v.mutate = func(inst *fcvm.Instance, r *runtimeadmission.Receipt) {
				simulatedRPCSnapshotConsumption(v, inst, r)
				r.ArtifactConsumption.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(true)
			}
			created, err := s.CreateAdmittedRuntime(admittedRPCContext(t), boot)
			if err != nil {
				t.Fatal("paused measured RPC boot", err)
			}
			parent, err := runtimeadmission.ReceiptFromProto(created.Receipt)
			if err != nil {
				t.Fatal(err)
			}
			p := runtimeadmission.Promotion{Parent: parent, Binding: parent.Binding}
			p.Binding.Token, p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = uuid.NewString(), time.Now().UnixNano(), time.Now().Add(time.Minute).UnixNano()
			p.Binding.PayloadHash, err = runtimeadmission.HashPromotionPayload(p.ToProto())
			if err != nil {
				t.Fatal(err)
			}
			hash, err := runtimeadmission.HashSnapshotResumeParent(parent)
			if err != nil {
				t.Fatal(err)
			}
			v.mutate = func(inst *fcvm.Instance, r *runtimeadmission.Receipt) {
				clock := r.CompletedAtUnixNano
				r.SnapshotResumeEvidence = runtimeadmission.SnapshotResumeEvidence{Version: runtimeadmission.SnapshotResumeEvidenceVersion,
					Binding: p.Binding, ParentBinding: parent.Binding, ParentCompletedAtUnixNano: parent.CompletedAtUnixNano, ParentReceiptHash: hash,
					ResumeCommandHash: runtimeadmission.SnapshotResumeCommandHash(), ResumeHookPayloadHash: strings.Repeat("a", 64),
					CommandCompletedAtUnixNano: clock, HostTimeUnixNano: clock, HookCompletedAtUnixNano: clock, CompletedAtUnixNano: clock}
				switch fault {
				case "missing":
					r.SnapshotResumeEvidence = runtimeadmission.SnapshotResumeEvidence{}
				case "command":
					r.SnapshotResumeEvidence.ResumeCommandHash = ""
				case "grant":
					r.SnapshotResumeEvidence.Binding.Token = uuid.NewString()
				case "parent":
					r.SnapshotResumeEvidence.ParentBinding.Token = uuid.NewString()
				case "process":
					r.ArtifactConsumption.ProcessStart += "1"
				case "mapping":
					r.SnapshotConsumption.MappedMemoryBytes--
				}
			}
			req := p.ToProto()
			if fault == "unsupported" {
				v.identity.SnapshotRestoreVersion = 0
			}
			if fault == "unknown-parent" {
				req.Parent.Binding.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			}
			resp, err := s.PromoteAdmittedRuntime(admittedRPCContext(t), req)
			if fault == "complete" {
				got, decodeErr := runtimeadmission.ReceiptFromProto(resp.GetReceipt())
				if err != nil || decodeErr != nil || p.CheckReceipt(got, time.Now()) != nil || len(v.destroyed) != 0 {
					t.Fatal("RPC lost measured resume lineage", err, decodeErr)
				}
			} else {
				wantDestroyed := 1
				if fault == "unknown-parent" || fault == "unsupported" {
					wantDestroyed = 0
				}
				if err == nil || resp != nil || len(v.destroyed) != wantDestroyed {
					t.Fatal("RPC accepted or leaked altered proof", fault, err)
				}
			}
		})
	}
}
