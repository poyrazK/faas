package vmmdgrpc_test

import (
	"net/netip"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type serviceCallerVMM struct{ *appTaskRuntimeVMM }

func (*serviceCallerVMM) ResolveServiceCaller(address netip.Addr) (fcvm.ServiceCallerIdentity, bool) {
	return fcvm.ServiceCallerIdentity{InstanceID: "task", AccountID: "account", AppID: "caller", DeploymentID: "deployment", AppTask: true}, address.String() == "10.100.0.7"
}

func TestServiceCallerIdentityRPC(t *testing.T) {
	client := newExecutionClient(t, &serviceCallerVMM{&appTaskRuntimeVMM{fakeVMM: &fakeVMM{}}})
	got, err := client.ResolveServiceCaller(t.Context(), &vmmdpb.ResolveServiceCallerRequest{SourceIp: "10.100.0.7"})
	if err != nil || !got.GetMatched() || !got.GetAppTask() || got.GetInstanceId() != "task" || got.GetAppId() != "caller" || got.GetDeploymentId() != "deployment" {
		t.Fatalf("identity RPC = %+v, %v", got, err)
	}
	got, err = client.ResolveServiceCaller(t.Context(), &vmmdpb.ResolveServiceCallerRequest{SourceIp: "10.100.0.8"})
	if err != nil || got.GetMatched() {
		t.Fatalf("unknown address matched: %+v, %v", got, err)
	}
	for _, address := range []string{"bad", "2001:db8::1"} {
		_, err = client.ResolveServiceCaller(t.Context(), &vmmdpb.ResolveServiceCallerRequest{SourceIp: address})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid address %s = %v", address, err)
		}
	}
	old := newExecutionClient(t, &fakeVMM{})
	if _, err := old.ResolveServiceCaller(t.Context(), &vmmdpb.ResolveServiceCallerRequest{SourceIp: "10.100.0.7"}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("missing provider = %v", err)
	}
}

func TestManagedServiceProbeRestoreRequiresLiveIdentityProvider(t *testing.T) {
	request := &vmmdpb.RestoreAppTaskRequest{Instance: "task", AccountId: "account", DeploymentId: "deployment", Plan: "pro",
		App: &vmmdpb.AppSpec{AppId: "caller", Runtime: "node22", BaseKey: "base", LayerKey: "layer", VcpuCount: 2, MemSizeMib: 256, CpuMillicores: 500}}
	old := &appTaskRuntimeVMM{fakeVMM: &fakeVMM{}}
	client := newExecutionClient(t, old)
	if _, err := client.RestoreManagedServiceProbe(t.Context(), request); status.Code(err) != codes.Unimplemented || old.wake.Instance != "" {
		t.Fatalf("incompatible provider received managed boot: %v, %+v", err, old.wake)
	}
	current := &serviceCallerVMM{&appTaskRuntimeVMM{fakeVMM: &fakeVMM{}}}
	client = newExecutionClient(t, current)
	response, err := client.RestoreManagedServiceProbe(t.Context(), request)
	if err != nil || response.GetInstance() != "task" || current.wake.DeploymentID != "deployment" {
		t.Fatalf("managed restore = %+v, %v, %+v", response, err, current.wake)
	}
}
