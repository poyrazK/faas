package state

// adr: 595 Actual stores fence new capture authority from a simulated serving restore.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func standardServingRestoreCapture(t *testing.T, s standardSnapshotPublicationTestStore, mode, variant string) {
	t.Helper()
	f := standardRestoreTestFixture(t, s)
	b, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding)
	if err != nil {
		t.Fatal(err)
	}
	parent := standardConsumedRestoreReceipt(b, f)
	ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, StateRunning, parent)
	if err != nil {
		t.Fatal("publish simulated serving restore", err)
	}
	if variant == "collected input cache" {
		if _, err := s.DeleteSnapshotsByID(t.Context(), []string{f.Snapshot.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "park" {
		if err := s.UpdateInstanceStateWithTimestamp(t.Context(), ins.ID, string(StateSnapshotting), time.Now()); err != nil {
			t.Fatal(err)
		}
		ins.State = string(StateSnapshotting)
	}
	token := uuid.NewString()
	prefix := strings.TrimSuffix(SnapshotCaptureMemKey(b.DeploymentID, mode, token), "mem")
	req := ApplicationStandardSnapshotCaptureRequest{Token: token, InstanceID: ins.ID, MemoryKey: prefix + "mem", VMStateKey: prefix + "vmstate", PrivateDriveKey: prefix + "drive",
		FCVersion: f.Grant.FCVersion, Mode: mode, SourceStartedAtUnixNano: ins.StartedAt.UnixNano()}
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil || !g.Parent.Equal(parent) || g.Token == parent.SnapshotConsumption.CaptureToken {
		t.Fatal("fresh capture did not retain the actual serving receipt", err)
	}
	a := standardSnapshotAck(g)
	a.Capture.Memory.Bytes = int64(ins.RAMMB) << 20
	if variant == "wrong parent" {
		a.Capture.Parent = f.Grant.Parent.Clone()
	}
	if variant == "changed policy" {
		policy := api.AppSecurityPolicyEnforce
		if _, err := s.UpdateApp(t.Context(), ins.AppID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &policy}); err != nil {
			t.Fatal(err)
		}
	}
	err = s.PublishApplicationStandardSnapshotCapture(t.Context(), a)
	if variant == "wrong parent" || variant == "changed policy" {
		if !errors.Is(err, ErrApplicationStandardRuntimeStale) || standardSnapshotGet(t, s, g).Acknowledgment != nil {
			t.Fatal("stale parent or current policy published new capture", err)
		}
		return
	}
	if err != nil || !standardSnapshotGet(t, s, g).Acknowledgment.Capture.Parent.Equal(parent) {
		t.Fatal("serving restored lineage lost at acknowledgment", err)
	}
	// The cache has one row per deployment/tier. Its owner retires the old
	// warm slot separately after new capture acknowledgment; lineage survives.
	if mode == "warm" && variant == "complete" {
		if count, err := s.DeleteSnapshotsByID(t.Context(), []string{f.Snapshot.ID}); err != nil || count != 1 {
			t.Fatal("retire previous warm cache slot", count, err)
		}
	}
	snap, err := s.PublishSnapshotIfRuntimeFresh(t.Context(), standardSnapshotCacheRow(ins.DeploymentID, g, a), ins.ID, ins.StartedAt)
	if err != nil || snap.ApplicationStandardCaptureToken != g.Token {
		t.Fatal("new cache did not retain serving-parent lineage", err)
	}
	if !standardSnapshotGet(t, s, f.Grant).Acknowledgment.Capture.Equal(f.Evidence.Capture) {
		t.Fatal("new capture rewrote its historical input catalog")
	}
}

func standardServingRestoreCaptureCases(t *testing.T, newStore func(*testing.T) standardSnapshotPublicationTestStore) {
	t.Helper()
	for _, mode := range []string{"warm", "park"} {
		for _, variant := range []string{"complete", "collected input cache", "wrong parent", "changed policy"} {
			t.Run(mode+"/"+variant, func(t *testing.T) { standardServingRestoreCapture(t, newStore(t), mode, variant) })
		}
	}
}

func TestMemStandardServingRestoreCapture(t *testing.T) {
	standardServingRestoreCaptureCases(t, func(*testing.T) standardSnapshotPublicationTestStore { return NewMemStore() })
}
