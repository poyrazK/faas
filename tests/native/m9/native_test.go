//go:build linux && metal

// spec: §14.A
// adr: 1529
// adr: 1530
package m9

import (
	"context"
	"errors"
	"os"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationAcknowledgementRecovery(t *testing.T) {
	if os.Getenv("FAAS_M9_QUALIFY") != "1" {
		t.Skip("native M9 is opt-in; use make native-m9-acceptance on the designated pair")
	}
	if os.Geteuid() != 0 || !CanonicalUUID(os.Getenv("FAAS_M9_RUN_ID")) ||
		!regexp.MustCompile(`^[0-9a-f]{40,64}$`).MatchString(os.Getenv("FAAS_M9_REVISION")) {
		t.Fatal("native M9 requires root, a run UUID and the gate's checkout revision")
	}
	if info, err := os.Lstat("/etc/faas/m9-acceptance-host"); err != nil || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o022 != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Fatal("native M9 requires the designated control-plane acceptance marker")
	}
	config, err := os.Open(os.Getenv("FAAS_M9_CONFIG")) //nolint:forbidigo // The root gate freezes this operator config in its private evidence directory; it is not a customer path.
	require(t, err)
	c, err := DecodeConfig(config)
	require(t, config.Close())
	require(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(c.TimeoutSeconds)*time.Second)
	// Register first, so nativeRuntime.cleanup runs before its tunnels close.
	t.Cleanup(cancel)
	r := newRuntime(t, ctx, c)
	var initial, moved, replacement state.Instance
	var original, retired state.MigrationSource
	var receipt fixtureReceipt
	var oldGeneration string
	var retainedCapacity map[string]int64
	firstOperation, secondOperation := uuid.NewString(), uuid.NewString()

	phase(t, "preflight", func(t *testing.T) {
		emitProof(t, "preflight", r.preflight(t, ctx))
	})
	phase(t, "fixture-boot", func(t *testing.T) {
		wake, err := r.engines[r.source.nodeID].Wake(ctx, c.AppID, c.DeploymentID, state.DefaultEnvScope, "native-m9")
		require(t, err)
		r.instance = wake.InstanceID
		r.ownedWakes[wake.WakeID] = true
		if wake.NodeID != r.source.nodeID || !CanonicalUUID(wake.InstanceID) || !CanonicalUUID(wake.WakeID) {
			t.Fatal("qualification fixture did not boot on the designated source")
		}
		initial, err = oneOwner(ctx, r, r.source.nodeID, wake.WakeID)
		require(t, err)
		receipt, err = probe(ctx, &r.source, r.instance, c.FixtureID, firstOperation)
		require(t, err)
		emitProof(t, "fixture-boot", map[string]any{"instance_id": r.instance, "wake_id": initial.WakeID,
			"node_id": initial.NodeID, "probe": receipt})
	})
	phase(t, "lost-acknowledgement", func(t *testing.T) {
		fault, err := NewAckFault(r.instance, FaultHooks{
			PauseSource: func(ctx context.Context) error {
				_, err := r.source.action(ctx, "pause")
				return err
			},
			BeforeAck: func(ctx context.Context, id, lease string) error {
				source, err := r.store.MigrationSourceByLease(ctx, lease)
				if err != nil {
					return err
				}
				if source.InstanceID != id || source.SourceNodeID != r.source.nodeID ||
					source.SourceWakeID != initial.WakeID || source.Phase != state.MigrationSourceRetiring || source.Proof != "" {
					return errors.New("source was not durably retiring before acknowledgement")
				}
				ram, err := r.store.ComputeNodeUsedMB(ctx, r.source.nodeID)
				if err != nil {
					return err
				}
				cpu, err := r.store.ComputeNodeUsedCPUMillicoresByNode(ctx, []string{r.source.nodeID})
				if err != nil {
					return err
				}
				if ram < int64(source.RAMMB) || cpu[r.source.nodeID] < int64(source.CPUMillicores) {
					return errors.New("ownership commit prematurely released physical source capacity")
				}
				retainedCapacity = map[string]int64{"ram_mb": ram, "cpu_millicores": cpu[r.source.nodeID]}
				admission, wake := r.fault.Load().DestinationIdentity()
				incoming, err := r.store.MigrationAdmissionByID(ctx, admission)
				if err != nil {
					return err
				}
				if incoming.Phase != state.MigrationCommitted || incoming.LeaseToken != lease || incoming.DestinationWakeID != wake {
					return errors.New("source acknowledgement preceded durable destination ownership")
				}
				if _, err := oneOwner(ctx, r, r.dest.nodeID, wake); err != nil {
					return err
				}
				original = source
				return nil
			},
			ResumeSource: func(ctx context.Context) error {
				_, err := r.source.action(ctx, "resume")
				return err
			},
			AfterAck: func(ctx context.Context, _, lease string) error {
				source, err := r.store.MigrationSourceByLease(ctx, lease)
				if err != nil {
					return err
				}
				if source.MigrationSourceBinding != original.MigrationSourceBinding ||
					source.Phase != state.MigrationSourceRetired || source.Proof == "" {
					return errors.New("real source RPC did not retain original-generation cleanup proof")
				}
				retired = source
				return nil
			},
		})
		require(t, err)
		r.fault.Store(fault)
		require(t, r.engines[r.dest.nodeID].MigrateRecoveryInstance(ctx, r.instance))
		lease, discarded, err := fault.Result()
		require(t, err)
		if !discarded || lease != retired.LeaseToken {
			t.Fatal("required acknowledgement response loss was not injected")
		}
		_, wake := fault.DestinationIdentity()
		r.ownedWakes[wake] = true
		moved, err = oneOwner(ctx, r, r.dest.nodeID, wake)
		require(t, err)
		same, err := probe(ctx, &r.dest, r.instance, c.FixtureID, firstOperation)
		require(t, err)
		if same != receipt {
			t.Fatal("migration lost in-memory operation evidence or executed a replay twice")
		}
		emitProof(t, "lost-acknowledgement", map[string]any{"instance_id": r.instance, "source_before_ack": original,
			"source_after_ack": retired, "destination_node_id": moved.NodeID, "destination_wake_id": moved.WakeID,
			"response_discarded": discarded, "retained_capacity": retainedCapacity, "probe": same})
	})
	phase(t, "vmmd-restart", func(t *testing.T) {
		crash, err := r.source.action(ctx, "crash")
		require(t, err)
		require(t, r.source.waitReady(ctx))
		identity, err := r.source.client.RuntimeAdmissionIdentity(ctx, &vmmdpb.RuntimeAdmissionIdentityRequest{})
		require(t, err)
		if identity.GetNodeId() != r.source.nodeID || !CanonicalUUID(identity.GetIncarnation()) ||
			identity.GetIncarnation() == r.source.incarnation {
			t.Fatal("restarted vmmd lost its registered runtime identity")
		}
		after, err := r.store.MigrationSourceByLease(ctx, retired.LeaseToken)
		require(t, err)
		if after != retired {
			t.Fatal("vmmd replacement changed the retained source receipt")
		}
		crash["retained_source"], crash["runtime_incarnation"] = after, identity.GetIncarnation()
		emitProof(t, "vmmd-restart", crash)
	})
	phase(t, "receipt-replay", func(t *testing.T) {
		_, err := r.source.client.AcknowledgeMigration(ctx, &vmmdpb.AcknowledgeMigrationRequest{
			InstanceId: r.instance, LeaseToken: retired.LeaseToken})
		require(t, err)
		after, err := r.store.MigrationSourceByLease(ctx, retired.LeaseToken)
		require(t, err)
		if after != retired {
			t.Fatal("acknowledgement replay changed its immutable receipt")
		}
		same, err := probe(ctx, &r.dest, r.instance, c.FixtureID, firstOperation)
		require(t, err)
		if same != receipt {
			t.Fatal("daemon restart or receipt replay changed the serving workload")
		}
		require(t, r.waitEmpty(ctx, &r.source))
		emitProof(t, "receipt-replay", map[string]any{"source": after, "probe": same})
	})
	phase(t, "replacement-protection", func(t *testing.T) {
		require(t, r.engines[r.source.nodeID].MigrateRecoveryInstance(ctx, r.instance))
		r.adoptMu.Lock()
		adopted := r.adoptions[r.source.nodeID]
		r.adoptMu.Unlock()
		if adopted == nil || adopted.GetMethod() != vmmdpb.WakeMethod_WAKE_RESTORE ||
			!CanonicalUUID(adopted.GetWakeId()) || adopted.GetWakeId() == moved.WakeID {
			t.Fatal("return migration did not restore a distinct destination wake")
		}
		r.ownedWakes[adopted.GetWakeId()] = true
		replacement, err = oneOwner(ctx, r, r.source.nodeID, adopted.GetWakeId())
		require(t, err)
		generation, err := r.source.client.GetOperatorRestartGeneration(ctx, &vmmdpb.GetOperatorRestartGenerationRequest{Instance: r.instance})
		require(t, err)
		oldGeneration = generation.GetGeneration()
		if !CanonicalUUID(oldGeneration) || oldGeneration == retired.NativeGeneration {
			t.Fatal("return migration did not create a distinct native generation")
		}
		next, err := probe(ctx, &r.source, r.instance, c.FixtureID, secondOperation)
		require(t, err)
		if next.BootID != receipt.BootID || next.Sequence != receipt.UniqueOperations+1 || next.UniqueOperations != next.Sequence {
			t.Fatal("return migration cold-booted or duplicated a unique operation")
		}
		_, err = r.source.client.AcknowledgeMigration(ctx, &vmmdpb.AcknowledgeMigrationRequest{
			InstanceId: r.instance, LeaseToken: retired.LeaseToken})
		require(t, err)
		current, err := r.source.client.GetOperatorRestartGeneration(ctx, &vmmdpb.GetOperatorRestartGenerationRequest{Instance: r.instance})
		require(t, err)
		if current.GetGeneration() != oldGeneration {
			t.Fatal("old acknowledgement changed the replacement's native generation")
		}
		retained, err := r.store.MigrationSourceByLease(ctx, retired.LeaseToken)
		require(t, err)
		if retained != retired {
			t.Fatal("old acknowledgement changed its retained receipt after instance-name reuse")
		}
		_, err = oneOwner(ctx, r, r.source.nodeID, replacement.WakeID)
		require(t, err)
		same, err := probe(ctx, &r.source, r.instance, c.FixtureID, secondOperation)
		require(t, err)
		if same != next {
			t.Fatal("old acknowledgement disrupted the replacement or executed an operation twice")
		}
		emitProof(t, "replacement-protection", map[string]any{"instance_id": r.instance, "wake_id": replacement.WakeID,
			"native_generation": oldGeneration, "old_source_generation": retired.NativeGeneration, "probe": same})
	})
	phase(t, "fixture-cleanup", func(t *testing.T) {
		require(t, r.parkOwned(ctx))
		checks := map[string]any{}
		for _, p := range []*nativePeer{&r.source, &r.dest} {
			require(t, r.waitEmpty(ctx, p))
			leaks, err := p.action(ctx, "leaks")
			require(t, err)
			checks[p.config.Name] = leaks
		}
		r.cleaned = true
		emitProof(t, "fixture-cleanup", map[string]any{"instance_id": r.instance, "leaks_after": checks})
	})
}

func phase(t *testing.T, name string, run func(*testing.T)) {
	t.Helper()
	if !t.Run(name, run) {
		t.FailNow()
	}
}
