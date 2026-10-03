// Package state — issue #463 / ADR-070 / PR-C sidecar-aware billing
// broker. Reads the deployment row's `sidecars` jsonb column and
// returns the per-sidecar RAM slice that schedd's Request /
// reaper's InstanceInfo attach to their admissionMB() arithmetic
// via api.BillableRAMMBWithSidecars.
//
// Why a method on Store (not on Deployment directly): schedd, the
// reaper, and meterd's sampler all carry a deployment_id by the
// time they need the sidecar MBs, so the read path lives once on
// the store. The deployment row's sidecars column is jsonb
// (raw []byte — see pkg/state/types.go:701-714 and migration 00095
// / 00118); we decode just the ram_mb field per sidecar rather
// than the full pkg/api.Sidecar struct, because pkg/state cannot
// import pkg/api (the cycle direction). The decoder stays local
// to this file and uses an anonymous struct shape that mirrors
// the field tags we care about.
//
// Cap enforcement: the five-helper hard cap is owned by the schema
// CHECK on the `deployments.sidecars` column (migration 00095 +
// 00118) and by apid's Sidecar.Validate at the request boundary.
// PR-C trusts len(result) ≤ api.SidecarCapMax at every call site;
// no re-check here.
package state

import (
	"context"
	"encoding/json"
	"fmt"
)

// sidecarRAMShape is the minimal subset of pkg/api.Sidecar we need
// for the billing broker. We deliberately do NOT import pkg/api
// here (pkg/api ↔ pkg/state cycle, memory: pkg-api-cannot-import-pkg-state);
// the field tags mirror the wire shape on `deployments.sidecars`.
//
// Field names match pkg/api/dto.go::Sidecar verbatim so a future
// PR that splits the jsonb column into a normalized table can
// rewrite this helper without changing any callers.
type sidecarRAMShape struct {
	RamMB int `json:"ram_mb"`
}

// DeploymentSidecarRAMs (issue #463 / ADR-070 / PR-C) reads the
// per-deployment sidecar RAM slice from the jsonb column. The
// returned slice:
//
//   - is empty (nil) if the deployment has no sidecars — matching
//     the no-sidecar admission shape; BillableRAMMBWithSidecars
//     collapses to BillableRAMMB in that case.
//   - has length ≤ api.SidecarCapMax (= 5) by construction; the
//     schema CHECK enforces this server-side and apid re-checks at
//     the request boundary.
//   - carries each sidecar's ram_mb verbatim, including 0 (the
//     "inherit plan RAM" sentinel). A future PR can choose to
//     drop the zero entries before they reach BillableRAMMB; today
//     the helper preserves the wire shape so a misconfigured 0
//     sidecar is observable in billing the same way it is in
//     cgroup memory.max (= "absent", the PlanRAMMB ceiling).
//
// Returns a wrapped error on a JSON decode failure — the column
// is server-side validated so a malformed row is a schema bug,
// not a customer input. The decoder fails closed (returns nil + err)
// so schedd's Request carries SidecarMBs=nil and the math reverts
// to the no-sidecar form, which is the safer admission behavior
// (a transient sidecar-RAM 0 path that admits less is preferable to
// an over-admission that breaches the ceiling).
func (s *PgStore) DeploymentSidecarRAMs(ctx context.Context, deploymentID string) ([]int, error) {
	if deploymentID == "" {
		return nil, fmt.Errorf("state: DeploymentSidecarRAMs: empty deployment_id")
	}
	var raw []byte
	const q = `SELECT sidecars::text FROM deployments WHERE id = $1`
	if err := s.pool.QueryRow(ctx, q, deploymentID).Scan(&raw); err != nil {
		return nil, fmt.Errorf("state: DeploymentSidecarRAMs %q: %w", deploymentID, err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var rows []sidecarRAMShape
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("state: DeploymentSidecarRAMs %q: decode sidecars: %w", deploymentID, err)
	}
	out := make([]int, 0, len(rows))
	for i, r := range rows {
		out = append(out, r.RamMB)
		_ = i // reserved for future per-sidecar name lookup
	}
	return out, nil
}

// DeploymentSidecarRAMs on the in-memory twin mirrors the
// PgStore method so cmd/apid unit tests and memstore-backed
// schedd tests can exercise the same admission shape without a DB.
func (s *MemStore) DeploymentSidecarRAMs(_ context.Context, deploymentID string) ([]int, error) {
	if deploymentID == "" {
		return nil, fmt.Errorf("state: DeploymentSidecarRAMs: empty deployment_id")
	}
	d, ok := s.deployments[deploymentID]
	if !ok {
		return nil, nil // unknown deployment => no sidecars (legacy shape)
	}
	if len(d.Sidecars) == 0 {
		return nil, nil
	}
	var rows []sidecarRAMShape
	if err := json.Unmarshal(d.Sidecars, &rows); err != nil {
		return nil, fmt.Errorf("state: DeploymentSidecarRAMs %q: decode sidecars: %w", deploymentID, err)
	}
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.RamMB)
	}
	return out, nil
}
