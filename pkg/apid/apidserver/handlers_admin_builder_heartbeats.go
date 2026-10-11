// handlers_admin_builder_heartbeats.go — Commit 7 (P5) operator
// observability for the builderd fleet.
//
// GET /v1/admin/obs/builder-heartbeats returns the most-recent
// `builder_tick` heartbeat per compute node that has stamped one
// in the last hour (the operator dashboard renders this as the
// "builderd fleet" tab) plus the fleet-total in-flight build
// queue count.
//
// Why a dedicated endpoint rather than folding onto the existing
// /v1/admin/obs/nodes:
//
//	The existing endpoint surfaces the vmmd heartbeat (source =
//	'heartbeat_tick') which feeds schedd's staleness gate. The
//	builder_tick heartbeat is observability-only — a missing
//	builder_tick does NOT flip the node inactive — so the source
//	enum split is load-bearing. Mixing the two into one endpoint
//	would force the operator UI to filter by source on every
//	render.
//
// builderd publishes the builder_tick row independently of the build queue;
// a quiet builder is therefore distinguishable from a missing builder. The
// writer resolves the compute node registered by vmmd and retries naturally
// while a fresh compute box is still joining the fleet.
package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsBuilderHeartbeats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	rows, err := s.store.LatestBuilderHeartbeatStats(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list builder heartbeats"))
		return
	}
	queued, err := s.store.QueuedBuildsCount(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count queued builds"))
		return
	}
	items := make([]api.ObsBuilderHeartbeatRow, 0, len(rows))
	for _, row := range rows {
		items = append(items, api.ObsBuilderHeartbeatRow{
			NodeID:        row.NodeID,
			ReceivedAt:    row.ReceivedAt,
			CPUPct60s:     row.CPUPct60s,
			DiskUsedBytes: row.DiskUsedBytes,
		})
	}
	writeJSON(w, http.StatusOK, api.ObsBuilderHeartbeatListResponse{
		GeneratedAt:  time.Now().UTC(),
		Items:        items,
		QueuedBuilds: queued,
	})
}
