package apidserver

// Operator snapshot health report (ADR-20261122135018).
//
//	GET /v1/admin/obs/snapshot-health

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/snapshothealth"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsSnapshotHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	reader, ok := s.store.(state.SnapshotHealthReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("snapshot inventory unavailable"))
		return
	}
	in := snapshothealth.Input{Now: time.Now().UTC()}
	snaps, err := reader.OperatorSnapshotInventory(r.Context(), api.ObsSnapshotInventoryMax+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read snapshots"))
		return
	}
	if len(snaps) > api.ObsSnapshotInventoryMax {
		snaps, in.Truncated = snaps[:api.ObsSnapshotInventoryMax], true
	}
	deps, err := reader.OperatorLiveDeploymentsWithoutSnapshot(r.Context(), api.ObsSnapshotUnsnapshottedMax+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read live deployments"))
		return
	}
	if len(deps) > api.ObsSnapshotUnsnapshottedMax {
		deps, in.UnsnapshottedTruncated = deps[:api.ObsSnapshotUnsnapshottedMax], true
	}
	in.Snapshots, in.Unsnapshotted = snaps, deps
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, snapshothealth.Build(in))
}
