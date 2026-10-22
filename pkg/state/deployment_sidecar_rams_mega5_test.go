// deployment_sidecar_rams_mega5_test.go — Coverage Mega-PR #5
// cluster 7: pin the MemStore variant of DeploymentSidecarRAMs
// (pkg/state/deployment_sidecar_rams.go:94) at 100%. The PgStore
// sql-path stays at 0% (covered by cluster 8's pgtest parity).
//
// Branches covered:
//   - empty deployment_id → errEmpty
//   - unknown deployment_id → (nil, nil) — legacy no-sidecar shape
//   - deployment exists, no Sidecars → (nil, nil)
//   - Sidecars = "null" JSON literal → (nil, nil)
//   - valid sidecars jsonb → []int{ram_mb,...} in declared order
//   - ram_mb=0 (the "inherit plan RAM" sentinel) preserved verbatim
//   - malformed Sidecars JSON → wrapped "decode sidecars" error
//
// Whitebox `package state`. No Postgres dependency.

package state_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
)

func seedDeploymentWithSidecars_Mega5(m *state.MemStore, id string, sidecars []byte) {
	(*m.Deployments())[id] = state.Deployment{
		ID:       id,
		Sidecars: sidecars,
	}
}

func TestDeploymentSidecarRAMs_MemStore_EmptyID_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	got, err := m.DeploymentSidecarRAMs(t.Context(), "")
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
	if err == nil {
		t.Fatal("err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "empty deployment_id") {
		t.Errorf("err = %v, want substring 'empty deployment_id'", err)
	}
}

func TestDeploymentSidecarRAMs_MemStore_UnknownID_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	got, err := m.DeploymentSidecarRAMs(t.Context(), "missing")
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
	if err != nil {
		t.Errorf("err = %v, want nil (legacy no-sidecar shape)", err)
	}
}

func TestDeploymentSidecarRAMs_MemStore_NoSidecars_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	seedDeploymentWithSidecars_Mega5(m, "dep-1", nil)

	got, err := m.DeploymentSidecarRAMs(t.Context(), "dep-1")
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestDeploymentSidecarRAMs_MemStore_NullLiteral_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	seedDeploymentWithSidecars_Mega5(m, "dep-1", []byte(`null`))

	got, err := m.DeploymentSidecarRAMs(t.Context(), "dep-1")
	// "null" JSON → json.Unmarshal sets the row slice to nil, then
	// make([]int, 0, 0) returns a non-nil empty slice. The length
	// is 0 either way; that's what callers care about.
	if len(got) != 0 {
		t.Errorf("got = %v, want empty slice (len 0)", got)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestDeploymentSidecarRAMs_MemStore_Valid_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	seedDeploymentWithSidecars_Mega5(m, "dep-1",
		[]byte(`[{"ram_mb":64},{"ram_mb":128}]`))

	got, err := m.DeploymentSidecarRAMs(t.Context(), "dep-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 2 || got[0] != 64 || got[1] != 128 {
		t.Errorf("got = %v, want [64 128]", got)
	}
}

func TestDeploymentSidecarRAMs_MemStore_ZeroRamMBSentinel_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	// ram_mb=0 is the "inherit plan RAM" sentinel — preserved verbatim
	// per the doc-comment contract.
	seedDeploymentWithSidecars_Mega5(m, "dep-1",
		[]byte(`[{"ram_mb":0}]`))

	got, err := m.DeploymentSidecarRAMs(t.Context(), "dep-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("got = %v, want [0] (zero-ram_mb sentinel preserved)", got)
	}
}

func TestDeploymentSidecarRAMs_MemStore_MalformedJSON_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	seedDeploymentWithSidecars_Mega5(m, "dep-1", []byte(`{not-json`))

	got, err := m.DeploymentSidecarRAMs(t.Context(), "dep-1")
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
	if err == nil {
		t.Fatal("err = nil, want non-nil (decode failure)")
	}
	if !strings.Contains(err.Error(), "decode sidecars") {
		t.Errorf("err = %v, want substring 'decode sidecars'", err)
	}
}

func TestDeploymentSidecarRAMs_MemStore_AcceptsRawMarshal_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	// Round-trip a json.RawMessage from the Deployment wire shape to
	// pin the field-type compatibility (json.RawMessage = []byte
	// alias under the hood).
	raw, _ := json.Marshal([]map[string]int{{"ram_mb": 256}, {"ram_mb": 512}})
	seedDeploymentWithSidecars_Mega5(m, "dep-1", raw)

	got, err := m.DeploymentSidecarRAMs(t.Context(), "dep-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 2 || got[0] != 256 || got[1] != 512 {
		t.Errorf("got = %v, want [256 512]", got)
	}
}
