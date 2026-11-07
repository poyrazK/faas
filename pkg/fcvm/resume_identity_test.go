// adr: 20261107160000 — the resume hook carries the serving instance's identity.
package fcvm

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResumeIdentityFromAPIEnv(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{
		api.PlatformInstanceIDEnv: "ins-1", api.PlatformNodeIDEnv: "node-a", api.PlatformRegionEnv: "fsn", "LOG_LEVEL": "info",
	})
	if got, want := resumeIdentityFromAPIEnv(raw), (ResumeIdentity{InstanceID: "ins-1", NodeID: "node-a", Region: "fsn"}); got != want {
		t.Fatalf("identity = %+v, want %+v", got, want)
	}
	for _, bad := range [][]byte{nil, []byte("{"), []byte(`{"LOG_LEVEL":"info"}`)} {
		if got := resumeIdentityFromAPIEnv(bad); got != (ResumeIdentity{}) {
			t.Fatalf("identity from %q = %+v, want zero", bad, got)
		}
	}
}

// A restore carries the identity schedd assigned in this wake's api env, not
// the one baked into the snapshot.
func TestWakeRestoreResumeHookCarriesIdentity(t *testing.T) {
	mgr := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: "/k"}, "1.7.0", nil, nil)
	if _, err := mgr.Wake(context.Background(), WakeRequest{
		Instance: "restore-A", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby, Snapshot: usableSnapshot(),
		APIEnvEntries: []APIEnvEntry{
			{Key: api.PlatformInstanceIDEnv, Value: "restore-A"},
			{Key: api.PlatformNodeIDEnv, Value: "node-b"},
			{Key: api.PlatformRegionEnv, Value: "hel"},
		},
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	vmm := mgr.vmm.(*fakeVMM)
	if len(vmm.resumeHookCalls) != 1 {
		t.Fatalf("resume hook calls = %d, want 1", len(vmm.resumeHookCalls))
	}
	want := resumeIdentityWire{InstanceID: "restore-A", NodeID: "node-b", Region: "hel"}
	if got := vmm.resumeHookCalls[0].Identity; got == nil || *got != want {
		t.Fatalf("resume hook identity = %+v, want %+v", got, want)
	}
}

func TestResumeIdentityContext(t *testing.T) {
	ctx := context.Background()
	if resumeIdentityWireFromContext(ctx) != nil {
		t.Fatal("background context carries an identity")
	}
	if resumeIdentityWireFromContext(withResumeIdentity(ctx, ResumeIdentity{NodeID: "node-a"})) != nil {
		t.Fatal("an identity without an instance ID was attached")
	}
	wire := resumeIdentityWireFromContext(withResumeIdentity(ctx, ResumeIdentity{InstanceID: "ins-1", NodeID: "node-a", Region: "fsn"}))
	if wire == nil || *wire != (resumeIdentityWire{InstanceID: "ins-1", NodeID: "node-a", Region: "fsn"}) {
		t.Fatalf("wire = %+v", wire)
	}
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	// guest/init/listen_resume_linux.go decodes these exact keys.
	if string(data) != `{"instanceId":"ins-1","nodeId":"node-a","region":"fsn"}` {
		t.Fatalf("wire JSON = %s", data)
	}
}
