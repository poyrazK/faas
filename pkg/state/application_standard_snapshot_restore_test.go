package state

// Durable MemStore/PgStore authority tests. Captured bytes and native cold
// receipts are explicit simulations; these tests never execute a snapshot VM.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type standardRestoreFixture struct {
	Source, Target Instance
	Snapshot       Snapshot
	Grant          runtimeadmission.SnapshotGrant
	Evidence       runtimeadmission.SnapshotRestoreEvidence
	Binding        runtimeadmission.Binding
	Capture        InstanceApplicationStandardAdmission
}

func standardRestoreTestFixture(t *testing.T, s standardSnapshotPublicationTestStore) standardRestoreFixture {
	t.Helper()
	source, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), source.State, req)
	if err != nil {
		t.Fatal(err)
	}
	a := standardSnapshotAck(g)
	a.Capture.Memory.Bytes = int64(source.RAMMB) << 20
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	snap, err := s.PublishSnapshotIfRuntimeFresh(t.Context(), standardSnapshotCacheRow(source.DeploymentID, g, a), source.ID, source.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	e, err := StandardSnapshotRestoreEvidence(standardSnapshotGet(t, s, g))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), source.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(t.Context(), source.ID); err != nil {
		t.Fatal(err)
	}
	node, err := s.UpsertComputeNode(t.Context(), ComputeNode{Name: "restore-" + uuid.NewString(), TargetURL: "unix:///tmp/restore-" + uuid.NewString() + ".sock", VPCPUs: 4, VCPUBudget: 4 * api.CPUOvercommit, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatComputeNode(t.Context(), node.ID); err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateInstanceWithMode(t.Context(), source.AppID, source.DeploymentID, string(StateWaking), source.RAMMB, node.ID, uuid.NewString(), string(InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := g.Parent.Binding
	b.Token, b.InstanceID, b.NodeID, b.Incarnation = uuid.NewString(), target.ID, capture.NodeID, uuid.NewString()
	b.CapturedInputHash = capture.NativeInputHash
	b.SnapshotCaptureToken = e.CaptureToken
	b.SnapshotEvidenceHash, err = e.Hash()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	b.IssuedAtUnixNano, b.ExpiresAtUnixNano = now.UnixNano(), now.Add(api.ApplicationStandardRuntimeAdmissionTTL).UnixNano()
	registerConsumedNativeIdentity(t, s, b, runtimeadmission.ArtifactProtocolVersion)
	return standardRestoreFixture{Source: source, Target: target, Snapshot: snap, Grant: g, Evidence: e, Binding: b, Capture: capture}
}

func standardRestoreAuthority(t *testing.T, s standardSnapshotPublicationTestStore) {
	t.Helper()
	f := standardRestoreTestFixture(t, s)
	if f.Binding.NodeID == f.Grant.Parent.Binding.NodeID || f.Binding.CapturedInputHash == f.Grant.Parent.Binding.CapturedInputHash {
		t.Fatal("fixture did not cross a native node")
	}
	b, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding)
	if err != nil || b.SnapshotEvidenceHash != f.Binding.SnapshotEvidenceHash || b.Incarnation == f.Grant.Parent.Binding.Incarnation {
		t.Fatal("fresh cross-node grant refused", err)
	}
	retry, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding)
	if err != nil || retry != b {
		t.Fatal("retry renewed restore authority", err)
	}
	// Exercise the final fence with a simulated verified cold fallback. A
	// measured restore receipt is still unavailable and is not fabricated here.
	r := consumedNativeReceipt(b, f.Capture)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, StateRunning, r); err != nil {
		t.Fatal("current final publication refused", err)
	}
	saved := standardSnapshotGet(t, s, f.Grant)
	if !saved.Grant.Equal(f.Grant) || !saved.Acknowledgment.Capture.Equal(f.Evidence.Capture) {
		t.Fatal("fresh grant rewrote historical capture")
	}
}

func standardRestoreRefusal(t *testing.T, newStore func(*testing.T) standardSnapshotPublicationTestStore) {
	t.Helper()
	for _, tc := range []struct {
		name string
		edit func(*testing.T, standardSnapshotPublicationTestStore, *standardRestoreFixture)
	}{
		{"unknown capture", func(_ *testing.T, _ standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			f.Binding.SnapshotCaptureToken = uuid.NewString()
		}},
		{"different catalog hash", func(_ *testing.T, _ standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			f.Binding.SnapshotEvidenceHash = strings.Repeat("0", 64)
		}},
		{"caller changed memory digest", func(t *testing.T, _ standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			f.Evidence.Capture.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
			f.Binding.SnapshotEvidenceHash, _ = f.Evidence.Hash()
		}},
		{"caller changed private digest", func(t *testing.T, _ standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			f.Evidence.Capture.PrivateDrive.Digest = "sha256:" + strings.Repeat("0", 64)
			f.Binding.SnapshotEvidenceHash, _ = f.Evidence.Hash()
		}},
		{"stale cache", func(t *testing.T, s standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			if err := s.MarkSnapshotStale(t.Context(), f.Snapshot.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"removed cache", func(t *testing.T, s standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			if _, err := s.DeleteSnapshotsByID(t.Context(), []string{f.Snapshot.ID}); err != nil {
				t.Fatal(err)
			}
		}},
		{"GC cache", func(t *testing.T, s standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			if _, err := s.MarkOldSnapshotsStale(t.Context(), []string{f.Snapshot.ID}); err != nil {
				t.Fatal(err)
			}
		}},
		{"current RAM", func(t *testing.T, s standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			ram := f.Target.RAMMB * 2
			if _, err := s.UpdateApp(t.Context(), f.Target.AppID, UpdateAppParams{RAMMB: &ram}); err != nil {
				t.Fatal(err)
			}
		}},
		{"current security", func(t *testing.T, s standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			p := api.AppSecurityPolicyEnforce
			if _, err := s.UpdateApp(t.Context(), f.Target.AppID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &p}); err != nil {
				t.Fatal(err)
			}
		}},
		{"process restart", func(t *testing.T, s standardSnapshotPublicationTestStore, f *standardRestoreFixture) {
			b := f.Binding
			b.Incarnation = uuid.NewString()
			registerConsumedNativeIdentity(t, s, b, runtimeadmission.ArtifactProtocolVersion)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			f := standardRestoreTestFixture(t, s)
			tc.edit(t, s, &f)
			if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatal("stale catalog or inputs issued authority", err)
			}
			assertStandardRestoreNoGrant(t, s, f.Binding.Token)
			assertNativeBootUnpublished(t, s, f.Target)
		})
	}
}

func standardRestorePublicationRefusal(t *testing.T, newStore func(*testing.T) standardSnapshotPublicationTestStore) {
	t.Helper()
	for _, change := range []string{"stale", "delete", "RAM"} {
		t.Run(change, func(t *testing.T) {
			s := newStore(t)
			f := standardRestoreTestFixture(t, s)
			b, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "stale":
				err = s.MarkSnapshotStale(t.Context(), f.Snapshot.ID)
			case "delete":
				_, err = s.DeleteSnapshotsByID(t.Context(), []string{f.Snapshot.ID})
			case "RAM":
				ram := f.Target.RAMMB * 2
				_, err = s.UpdateApp(t.Context(), f.Target.AppID, UpdateAppParams{RAMMB: &ram})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, StateRunning, consumedNativeReceipt(b, f.Capture)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
				t.Fatal("delayed readiness bypassed final catalog fence", err)
			}
			assertNativeBootUnpublished(t, s, f.Target)
			if _, err := s.(InstanceApplicationStandardRuntimeReceiptStore).GetInstanceApplicationStandardRuntimeReceipt(t.Context(), f.Target.ID); err == nil {
				t.Fatal("refusal saved a native receipt")
			}
		})
	}
}

func assertStandardRestoreNoGrant(t *testing.T, s standardSnapshotPublicationTestStore, token string) {
	t.Helper()
	switch store := s.(type) {
	case *MemStore:
		store.mu.Lock()
		_, found := store.instanceApplicationStandardBoots[token]
		store.mu.Unlock()
		if found {
			t.Fatal("refusal saved a grant")
		}
	case *PgStore:
		var count int
		if err := store.pool.QueryRow(t.Context(), `SELECT count(*) FROM instance_application_standard_boots WHERE token=$1`, token).Scan(&count); err != nil || count != 0 {
			t.Fatal("refusal saved a grant", count, err)
		}
	default:
		t.Fatal("unverified store backend")
	}
}

func standardRestoreStableInputRefusal(t *testing.T) {
	t.Helper()
	s := NewMemStore()
	f := standardRestoreTestFixture(t, s)
	r := standardSnapshotGet(t, s, f.Grant)
	for _, field := range []string{"instance_ram_mb", "instance_mode", "account_plan", "settings", "runtime_artifacts"} {
		t.Run(field, func(t *testing.T) {
			changed := f.Capture
			changed.inputs = []byte(strings.Replace(string(changed.inputs), `"`+field+`":`, `"removed_`+field+`":`, 1))
			if checkStandardSnapshotRestoreBinding(f.Binding, changed, r, f.Snapshot, time.Now()) == nil {
				t.Fatal("missing stable runtime input accepted")
			}
		})
	}
	bad := f.Snapshot
	bad.MemBytes++
	if checkStandardSnapshotRestoreBinding(f.Binding, f.Capture, r, bad, time.Now()) == nil {
		t.Fatal("RAM byte count mismatch accepted")
	}
	r.Acknowledgment = nil
	r.ReceivedAt = time.Time{}
	if _, err := StandardSnapshotRestoreEvidence(r); err == nil {
		t.Fatal("unacknowledged catalog produced restore evidence")
	}
}

func TestMemStandardSnapshotRestoreAuthority(t *testing.T) {
	standardRestoreAuthority(t, NewMemStore())
}
func TestMemStandardSnapshotRestoreRefusal(t *testing.T) {
	standardRestoreRefusal(t, func(*testing.T) standardSnapshotPublicationTestStore { return NewMemStore() })
}
func TestMemStandardSnapshotRestoreFinalPublicationFence(t *testing.T) {
	standardRestorePublicationRefusal(t, func(*testing.T) standardSnapshotPublicationTestStore { return NewMemStore() })
}
func TestStandardSnapshotRestoreStableInputRefusal(t *testing.T) {
	standardRestoreStableInputRefusal(t)
}

func standardRestoreFreshTargetRefusal(t *testing.T, s standardSnapshotPublicationTestStore) {
	t.Helper()
	f := standardRestoreTestFixture(t, s)
	if err := s.UpdateInstanceStateToTerminal(t.Context(), f.Target.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(t.Context(), f.Target.ID); err != nil {
		t.Fatal(err)
	}
	p := api.AppSecurityPolicyWarn
	if _, err := s.UpdateApp(t.Context(), f.Target.AppID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &p}); err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstanceWithMode(t.Context(), f.Target.AppID, f.Target.DeploymentID, string(StateWaking), f.Target.RAMMB, f.Target.NodeID, uuid.NewString(), string(InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := f.Binding
	b.Token, b.InstanceID, b.CapturedInputHash = uuid.NewString(), ins.ID, capture.NativeInputHash
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("fresh target admitted old catalog policy", err)
	}
	assertStandardRestoreNoGrant(t, s, b.Token)
	// Current inputs independently authorize cold boot. The old capture never
	// becomes policy authority merely because the target's own capture is fresh.
	b.SnapshotCaptureToken, b.SnapshotEvidenceHash = "", ""
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, b); err != nil {
		t.Fatal("catalog refusal poisoned current cold authority", err)
	}
}

func TestMemStandardSnapshotRestoreFreshTargetRefusesOldPolicy(t *testing.T) {
	standardRestoreFreshTargetRefusal(t, NewMemStore())
}
