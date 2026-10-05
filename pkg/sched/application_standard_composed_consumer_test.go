package sched

// adr: 435, 581. All portable consumer facts remain process/revision bound.

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type composedWaveNativeVMM struct {
	*standardNativeTestVMM
	store    state.InstanceApplicationStandardAdmissionStore
	policies map[string]runtimeadmission.EgressPolicy
	receipts map[string]runtimeadmission.Receipt
	serial   uint32
}

func newComposedWaveNativeVMM(t *testing.T, s composedWaveStore) *composedWaveNativeVMM {
	t.Helper()
	identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ArtifactProtocolVersion, NodeID: standardNativeTestNodeID(t, s), Incarnation: uuid.NewString()}
	if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatComputeNode(t.Context(), identity.NodeID); err != nil {
		t.Fatal(err)
	}
	return &composedWaveNativeVMM{standardNativeTestVMM: &standardNativeTestVMM{fakeVMM: &fakeVMM{}, identity: identity}, store: s, policies: map[string]runtimeadmission.EgressPolicy{}, receipts: map[string]runtimeadmission.Receipt{}}
}

func (v *composedWaveNativeVMM) UpdateAppEgressPolicy(ctx context.Context, nodeID, appID string, revision int64, cidrs []netip.Prefix, ports []int) error {
	if err := v.standardNativeTestVMM.UpdateAppEgressPolicy(ctx, nodeID, appID, revision, cidrs, ports); err != nil {
		return err
	}
	v.policies[uuid.MustParse(appID).String()] = (runtimeadmission.EgressPolicy{AppID: uuid.MustParse(appID).String(), Revision: revision, Allowlist: cidrs, Ports: ports}).Clone()
	return nil
}

func (v *composedWaveNativeVMM) CreateAdmittedRuntime(ctx context.Context, nodeID string, request *vmmdpb.CreateAdmittedRuntimeRequest) (*WakeOutcome, error) {
	out, err := v.standardNativeTestVMM.CreateAdmittedRuntime(ctx, nodeID, request)
	if err != nil {
		return nil, err
	}
	r := out.RuntimeAdmissionReceipt
	capture, err := v.store.GetInstanceApplicationStandardAdmission(ctx, r.Binding.InstanceID)
	if err != nil {
		return nil, err
	}
	v.serial++
	out.HostIP, out.LeaseUID = fmt.Sprintf("10.100.0.%d", v.serial+10), int32(20000+v.serial)
	r.HostIP, r.LeaseUID = out.HostIP, out.LeaseUID
	r.ArtifactConsumption = runtimeadmission.ArtifactConsumption{ConfigHash: strings.Repeat("c", 64), ProcessPID: 42 + v.serial, ProcessStart: "101"}
	for _, a := range capture.RuntimeArtifacts {
		source := runtimeadmission.ArtifactSource{Kind: a.Kind, WorkloadName: a.WorkloadName, StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes}
		drive := runtimeadmission.ConsumedDrive{Source: source, DriveID: "drive-" + source.Role(), ReadOnly: source.Role() != "main", RootDevice: source.Role() == "base", ProducerDigest: source.Digest, ProducerBytes: source.Bytes, InjectedDigest: source.Digest, InjectedBytes: source.Bytes}
		if !drive.ReadOnly {
			drive.InjectedDigest = "sha256:" + strings.Repeat("d", 64)
		}
		r.ArtifactConsumption.Drives = append(r.ArtifactConsumption.Drives, drive)
	}
	v.receipts[r.Binding.InstanceID] = r.Clone()
	return out, nil
}

func (f *composedWaveFixture) assertRuntime(t *testing.T, id string, revision int64) state.Instance {
	t.Helper()
	rows, err := f.s.ListInstancesForApp(t.Context(), f.apps[id].ID)
	if err != nil {
		t.Fatal(err)
	}
	var live []state.Instance
	for _, row := range rows {
		if row.State == string(state.StateRunning) {
			live = append(live, row)
		}
	}
	if len(live) != 1 {
		t.Fatalf("service %s has %d running instances: %+v", id, len(live), rows)
	}
	c, err := f.s.GetInstanceApplicationStandardAdmission(t.Context(), live[0].ID)
	r := f.vmm.receipts[uuid.MustParse(live[0].ID).String()]
	if err != nil || c.DesiredRevision != revision || c.PersistedRevision != revision || len(c.RuntimeArtifacts) != 2 || c.RuntimeArtifacts[0].ProducerID != f.base.ID || r.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion || r.Binding.Incarnation != f.vmm.identity.Incarnation || r.ArtifactConsumption.Check(r.Binding.ArtifactSourcesHash) != nil {
		t.Fatalf("runtime omitted current capture, stable base or measured receipt: capture=%+v receipt=%+v error=%v", c, r, err)
	}
	e, err := f.s.GetApplicationStandardEnrollment(t.Context(), f.owner.PersonalOrg.ID, id)
	if err != nil || e.DesiredRevision == revision && (c.EffectiveHash != e.EffectiveHash || r.Binding.EffectiveHash != e.EffectiveHash || r.Binding.CapturedInputHash != c.NativeInputHash) {
		t.Fatal("current runtime did not capture its installed effective inputs", id, err)
	}
	if e.DesiredRevision == revision {
		stored, err := f.s.GetInstanceApplicationStandardRuntimeReceipt(t.Context(), live[0].ID)
		if err != nil || !stored.Equal(r) {
			t.Fatal("published runtime lost the actual native receipt", id, err)
		}
	}
	return live[0]
}

func (f *composedWaveFixture) restartLogging(t *testing.T) {
	t.Helper()
	for nodeID := range f.sessions {
		session, err := f.s.RegisterApplicationStandardLogConsumer(t.Context(), nodeID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		f.sessions[nodeID] = session
	}
}

func (f *composedWaveFixture) consume(t *testing.T) {
	t.Helper()
	snapshot, err := f.s.LoadApplicationStandardLogConsumerSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for id, app := range f.apps {
		roster, err := f.s.GetApplicationStandardConsumerRoster(t.Context(), app.OrgID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range roster.Nodes {
			if !node.LoggingRequired || node.LoggingStoppedAt != nil {
				continue
			}
			if err := f.s.HeartbeatComputeNode(t.Context(), node.NodeID); err != nil {
				t.Fatal(err)
			}
			session, ok := f.sessions[node.NodeID]
			if !ok {
				session, err = f.s.RegisterApplicationStandardLogConsumer(t.Context(), node.NodeID, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				f.sessions[node.NodeID] = session
			}
			loaded := 0
			for _, inventory := range snapshot.Inventories {
				if inventory.AppID == id {
					if _, err := f.s.RecordApplicationStandardLogInventory(t.Context(), session, inventory); err != nil {
						t.Fatal(err)
					}
					loaded++
				}
			}
			inventories, err := f.s.ListApplicationStandardLogInventories(t.Context(), app.OrgID, app.ID)
			if err != nil || loaded != 1 || len(inventories) != 1 || inventories[0].ApplicationStandardLogConsumerSession != session {
				t.Fatal("consumer omitted the current loaded inventory", id, loaded, inventories, err)
			}
			f.deliver(t, id, session, snapshot.Drains)
		}
	}
	f.consumeEgress(t)
}

func (f *composedWaveFixture) deliver(t *testing.T, id string, session state.ApplicationStandardLogConsumerSession, drains []state.AppLogDrain) {
	t.Helper()
	e, err := f.s.GetApplicationStandardEnrollment(t.Context(), f.owner.PersonalOrg.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	instance := f.assertRuntime(t, id, e.DesiredRevision)
	for _, d := range drains {
		if uuid.MustParse(d.AppID).String() != id || d.StandardBinding == nil {
			continue
		}
		key := session.SessionID + "/" + d.ID
		f.events[key]++
		sequence := f.events[key]
		if _, err := f.s.RecordApplicationStandardLogDelivery(t.Context(), d, instance.ID, uint64(sequence)); err != nil {
			t.Fatal("delivery", err)
		}
		if _, err := f.s.RecordApplicationStandardLogHealth(t.Context(), session, d, state.ApplicationStandardLogHealthEvent{EventRevision: sequence, Status: "healthy", Reason: "delivered", SourceInstanceID: instance.ID, Sequence: sequence}); err != nil {
			t.Fatal("provider receipt", err)
		}
	}
}

func (f *composedWaveFixture) consumeEgress(t *testing.T) {
	t.Helper()
	targets, err := f.s.ListPendingApplicationStandardEgress(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if _, owned := f.apps[target.AppID]; !owned {
			continue
		}
		policy, ok := f.vmm.policies[target.AppID]
		hash, err := policy.Hash()
		if !ok || err != nil || hash != target.PolicyHash || target.Identity != f.vmm.identity {
			t.Fatal("simulated native policy did not match current target", target, err)
		}
		if _, err := f.s.RecordApplicationStandardEgress(t.Context(), target, runtimeadmission.EgressReceipt{Identity: f.vmm.identity, AppID: policy.AppID, Revision: policy.Revision, PolicyHash: hash}); err != nil {
			t.Fatal(err)
		}
	}
	for id := range f.apps {
		rows, err := f.s.ListApplicationStandardEgress(t.Context(), f.owner.PersonalOrg.ID, id)
		if err != nil || len(rows) != 1 || rows[0].Target.Identity != f.vmm.identity {
			t.Fatal("service omitted current native egress receipt", id, rows, err)
		}
	}
}
