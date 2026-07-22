// Tests for registerComputeNode (issue #98 / ADR-028). The happy
// path covers upsert + re-upsert idempotency; the failure paths
// pin the validation contract (zero values are rejected) and the
// "operator opted out" path (empty NodeName = no DB needed).

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRegisterComputeNode_HappyPath(t *testing.T) {
	st := state.NewMemStore()
	cfg := ComputeNodeConfig{
		NodeName:           "box-east-1",
		VPCPUs:             160,
		MemMB:              56000,
		MaxConcurrency:     200,
		AdmissionCeilingMB: 47600,
	}
	got, err := registerComputeNode(context.Background(), st, cfg, "unix:///run/faas/vmmd.sock",
		func(context.Context) (string, error) { return "", nil }, testLogger())
	if err != nil {
		t.Fatalf("registerComputeNode: %v", err)
	}
	if got.Name != "box-east-1" {
		t.Errorf("name = %q", got.Name)
	}
	if got.ID == "" {
		t.Error("id empty")
	}
	if !got.Active {
		t.Error("not active after registration")
	}
	if got.TargetURL != "unix:///run/faas/vmmd.sock" {
		t.Errorf("target_url = %q", got.TargetURL)
	}
}

// TestRegisterComputeNode_Idempotent: a second call with the same
// name returns the same id (upsert, not insert). This is the
// "vmmd reboots and schedd still knows me" path.
func TestRegisterComputeNode_Idempotent(t *testing.T) {
	st := state.NewMemStore()
	cfg := ComputeNodeConfig{
		NodeName: "box-east-1",
		VPCPUs:   160, MemMB: 56000,
		MaxConcurrency: 200, AdmissionCeilingMB: 47600,
	}
	first, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("id changed across upsert: %q -> %q", first.ID, second.ID)
	}
	if second.Active != true {
		t.Error("upsert did not re-activate")
	}
}

// TestRegisterComputeNode_EmptyNameSkips: the legacy default-local
// path. No DB calls; no error. This is what tests / single-box dev
// rely on.
func TestRegisterComputeNode_EmptyNameSkips(t *testing.T) {
	st := state.NewMemStore()
	got, err := registerComputeNode(context.Background(), st,
		ComputeNodeConfig{}, "unix:///x", nil, testLogger())
	if err != nil {
		t.Fatalf("empty name: %v", err)
	}
	if got.Name != "" {
		t.Errorf("empty-name path returned a row: %+v", got)
	}
}

// TestRegisterComputeNode_RejectsZeroFields: any zero-valued resource
// number is a config bug; vmmd must fail fast at startup rather than
// register a node with bogus capacity.
func TestRegisterComputeNode_RejectsZeroFields(t *testing.T) {
	st := state.NewMemStore()
	cases := []ComputeNodeConfig{
		{NodeName: "x", VPCPUs: 0, MemMB: 1, MaxConcurrency: 1, AdmissionCeilingMB: 1},
		{NodeName: "x", VPCPUs: 1, MemMB: 0, MaxConcurrency: 1, AdmissionCeilingMB: 1},
		{NodeName: "x", VPCPUs: 1, MemMB: 1, MaxConcurrency: 0, AdmissionCeilingMB: 1},
		{NodeName: "x", VPCPUs: 1, MemMB: 1, MaxConcurrency: 1, AdmissionCeilingMB: 0},
	}
	for i, cfg := range cases {
		_, err := registerComputeNode(context.Background(), st, cfg, "unix:///x", nil, testLogger())
		if err == nil {
			t.Errorf("case %d: expected zero-field rejection", i)
		}
	}
}

// TestRegisterComputeNode_OverlayDetectionErrorContinues: a tailscale
// detection failure logs a warning and proceeds without the IP
// rather than failing vmmd startup. This matters for single-box dev
// where tailscale isn't installed and the daemon should still
// register via the unix target_url.
func TestRegisterComputeNode_OverlayDetectionErrorContinues(t *testing.T) {
	st := state.NewMemStore()
	detector := func(context.Context) (string, error) {
		return "", errors.New("tailscale down")
	}
	got, err := registerComputeNode(context.Background(), st,
		ComputeNodeConfig{
			NodeName: "box-east-1", VPCPUs: 1, MemMB: 1024,
			MaxConcurrency: 1, AdmissionCeilingMB: 512,
		}, "tcp://100.64.0.1:50051", detector, testLogger())
	if err != nil {
		t.Fatalf("overlay failure should not block registration: %v", err)
	}
	if got.Name != "box-east-1" {
		t.Errorf("name = %q", got.Name)
	}
}
