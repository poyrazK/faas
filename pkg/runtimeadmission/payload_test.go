package runtimeadmission

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/protobuf/proto"
)

func payloadFixture() *vmmdpb.CreateAdmittedRuntimeRequest {
	return &vmmdpb.CreateAdmittedRuntimeRequest{Binding: &vmmdpb.RuntimeBootBinding{Token: "excluded"}, Boot: &vmmdpb.CreateAdmittedRuntimeRequest_Restore{Restore: &vmmdpb.CreateFromSnapshotRequest{Instance: "instance", WakeId: "wake", App: &vmmdpb.AppSpec{BaseKey: "base", LayerKey: "layer", AppId: "app", EgressAllowlist: []string{"8.8.8.0/24"}, EgressPorts: []uint32{5432}, SealedEnv: []*vmmdpb.SealedSecret{{Key: "key", Ciphertext: []byte("sealed")}}, Sidecars: []*vmmdpb.SidecarSpec{{Name: "cache", StorageKey: "sidecar"}}}, Snapshot: &vmmdpb.SnapshotRef{StorageKey: "memory", VmstateStorageKey: "state", DeploymentId: "deployment"}}}}
}

func TestBootPayloadHashCoversAllDeliveredInputs(t *testing.T) {
	base := payloadFixture()
	want, err := HashBootPayload(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*vmmdpb.CreateAdmittedRuntimeRequest)
	}{
		{"instance", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetRestore().Instance = "other" }},
		{"sealed env", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetRestore().App.SealedEnv[0].Ciphertext[0]++ }},
		{"sidecar", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetRestore().App.Sidecars[0].StorageKey = "other" }},
		{"network", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetRestore().App.EgressPorts = []uint32{6379} }},
		{"snapshot", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetRestore().Snapshot.VmstateStorageKey = "other" }},
		{"paused", func(r *vmmdpb.CreateAdmittedRuntimeRequest) { r.GetRestore().KeepPaused = true }},
		{"variant", func(r *vmmdpb.CreateAdmittedRuntimeRequest) {
			r.Boot = &vmmdpb.CreateAdmittedRuntimeRequest_ColdBoot{ColdBoot: &vmmdpb.CreateColdBootRequest{Instance: "instance"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := proto.Clone(base).(*vmmdpb.CreateAdmittedRuntimeRequest)
			test.mutate(copy)
			hash, err := HashBootPayload(copy)
			if err != nil || hash == want {
				t.Fatalf("changed payload hash=%s err=%v", hash, err)
			}
		})
	}
	base.Binding.Token = "another grant"
	if hash, err := HashBootPayload(base); err != nil || hash != want {
		t.Fatal("binding participates in self-referential hash")
	}
}

func TestBootPayloadRejectsUnknownControlsRecursively(t *testing.T) {
	for _, test := range []struct {
		name   string
		target func(*vmmdpb.CreateAdmittedRuntimeRequest) proto.Message
	}{
		{"envelope", func(r *vmmdpb.CreateAdmittedRuntimeRequest) proto.Message { return r }},
		{"binding", func(r *vmmdpb.CreateAdmittedRuntimeRequest) proto.Message { return r.Binding }},
		{"restore", func(r *vmmdpb.CreateAdmittedRuntimeRequest) proto.Message { return r.GetRestore() }},
		{"app", func(r *vmmdpb.CreateAdmittedRuntimeRequest) proto.Message { return r.GetRestore().App }},
		{"secret", func(r *vmmdpb.CreateAdmittedRuntimeRequest) proto.Message { return r.GetRestore().App.SealedEnv[0] }},
		{"sidecar", func(r *vmmdpb.CreateAdmittedRuntimeRequest) proto.Message { return r.GetRestore().App.Sidecars[0] }},
		{"snapshot", func(r *vmmdpb.CreateAdmittedRuntimeRequest) proto.Message { return r.GetRestore().Snapshot }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := payloadFixture()
			test.target(r).ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
			if _, err := HashBootPayload(r); err == nil {
				t.Fatal("unrecognized control hashed and ignored")
			}
		})
	}
}
