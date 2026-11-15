package sched_test

// adr: 20261110210000

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// snapshotSealingVMM seals park and warm captures and records the ids.
type snapshotSealingVMM struct {
	*sched.WarmResumeFakeVMM
	mu     sync.Mutex
	parks  []string
	warms  []string
	before []bool
}

func (s *snapshotSealingVMM) PauseAndSnapshotSealed(_ context.Context, _, _, _, _ string, before bool, captureID string) (sched.SnapshotBytes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parks, s.before = append(s.parks, captureID), append(s.before, before)
	return sched.SnapshotBytes{MemBytes: 1 << 20, SealedKey: []byte("sealed:" + captureID), SealCaptureID: captureID}, nil
}

func (s *snapshotSealingVMM) WarmSnapshotSealed(_ context.Context, _, _, _, _, captureID string) (sched.SnapshotBytes, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warms = append(s.warms, captureID)
	return sched.SnapshotBytes{MemBytes: 1 << 20}, []byte("sealed:" + captureID), nil
}

func sealingEngine(t *testing.T, vmm sched.RoutedVMM, sealed bool) (*sched.Engine, state.Instance) {
	t.Helper()
	store := state.NewMemStore()
	app, err := store.CreateApp(context.Background(), state.App{AccountID: uuid.NewString(), Slug: "sealed-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	e, err := sched.NewEngine(context.Background(), store, sched.NewNodeLedger(), vmm, nil, "1.10.0", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e.WithSealedSnapshots(sealed), state.Instance{ID: uuid.NewString(), AppID: app.ID, DeploymentID: uuid.NewString(), NodeID: uuid.NewString()}
}

func TestCaptureSnapshot_Sealing(t *testing.T) {
	token := uuid.NewString()
	dep := uuid.NewString()
	parkKey := state.SnapshotCaptureMemKey(dep, state.SnapshotTierInit, token)
	warmKey := state.SnapshotCaptureMemKey(dep, state.SnapshotTierWarm, token)
	for _, tc := range []struct {
		name               string
		sealed             bool
		mode               string
		memKey, stateKey   string
		wantSealed         bool
		wantPlain, wantWrm int
	}{
		{name: "off publishes plaintext", mode: "park", memKey: parkKey, stateKey: parkKey + "-state", wantPlain: 1},
		{name: "park sealed with the key's token", sealed: true, mode: "park", memKey: parkKey, stateKey: parkKey + "-state", wantSealed: true},
		{name: "warm sealed with the key's token", sealed: true, mode: "warm", memKey: warmKey, stateKey: warmKey + "-state", wantSealed: true},
		{name: "host-path vmstate stays plaintext", sealed: true, mode: "park", memKey: parkKey, wantPlain: 1},
		{name: "legacy key without a token stays plaintext", sealed: true, mode: "park", memKey: state.SnapMemKey(dep), stateKey: "snap/" + dep + "/vmstate", wantPlain: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := &snapshotSealingVMM{WarmResumeFakeVMM: sched.NewWarmResumeFakeVMM()}
			e, ins := sealingEngine(t, vmm, tc.sealed)
			b, err := e.CaptureSnapshotWithStandards(context.Background(), ins, "/srv/fc/vmstate", tc.memKey, tc.stateKey, true, tc.mode)
			if err != nil {
				t.Fatalf("capture: %v", err)
			}
			park, warm := vmm.PlainSnapshotCounts()
			if park+warm != tc.wantPlain+tc.wantWrm {
				t.Fatalf("plaintext captures park=%d warm=%d, want %d", park, warm, tc.wantPlain)
			}
			if !tc.wantSealed {
				if len(b.SealedKey) != 0 || len(vmm.parks)+len(vmm.warms) != 0 {
					t.Fatalf("unexpected seal: key=%q parks=%v warms=%v", b.SealedKey, vmm.parks, vmm.warms)
				}
				return
			}
			if b.SealCaptureID != token || string(b.SealedKey) != "sealed:"+token {
				t.Fatalf("sealed bytes = id %q key %q, want the key's token %s", b.SealCaptureID, b.SealedKey, token)
			}
			if tc.mode == "park" && (len(vmm.parks) != 1 || !vmm.before[0]) {
				t.Fatalf("park seals = %v before=%v, want one with before_checkpoint", vmm.parks, vmm.before)
			}
			if tc.mode == "warm" && len(vmm.warms) != 1 {
				t.Fatalf("warm seals = %v, want one", vmm.warms)
			}
		})
	}
}

func TestCaptureSnapshot_SealingRefusesAVMMThatCannotSeal(t *testing.T) {
	vmm := sched.NewWarmResumeFakeVMM()
	e, ins := sealingEngine(t, vmm, true)
	key := state.SnapshotCaptureMemKey(ins.DeploymentID, state.SnapshotTierInit, uuid.NewString())
	if _, err := e.CaptureSnapshotWithStandards(context.Background(), ins, "", key, key+"-state", false, "park"); err == nil {
		t.Fatal("capture without a sealing VMM succeeded, want a refusal")
	}
	if park, warm := vmm.PlainSnapshotCounts(); park+warm != 0 {
		t.Fatalf("a plaintext capture was taken (park=%d warm=%d)", park, warm)
	}
}

func TestSnapshotWrittenPayload_CarriesTheSeal(t *testing.T) {
	e, ins := sealingEngine(t, sched.NewWarmResumeFakeVMM(), true)
	token := uuid.NewString()
	key := state.SnapshotCaptureMemKey(ins.DeploymentID, state.SnapshotTierInit, token)
	var got struct {
		SealedKey       []byte `json:"sealed_key"`
		SealedCaptureID string `json:"sealed_capture_id"`
	}
	raw := e.SnapshotWrittenPayload(context.Background(), ins.DeploymentID, key, sched.SnapshotBytes{SealedKey: []byte("k"), SealCaptureID: token}, state.SnapshotTierInit)
	if err := json.Unmarshal(raw, &got); err != nil || string(got.SealedKey) != "k" || got.SealedCaptureID != token {
		t.Fatalf("payload seal = %+v err=%v", got, err)
	}
	plain := e.SnapshotWrittenPayload(context.Background(), ins.DeploymentID, key, sched.SnapshotBytes{}, state.SnapshotTierInit)
	var fields map[string]any
	if err := json.Unmarshal(plain, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["sealed_key"]; ok {
		t.Fatalf("plaintext payload carries a sealed_key: %s", plain)
	}
}
