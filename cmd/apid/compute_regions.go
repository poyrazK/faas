package main

import (
	"context"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listComputeRegions(w http.ResponseWriter, r *http.Request, _ state.Account) {
	nodes, err := s.store.ActiveComputeNodes(r.Context())
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Regions unavailable", "could not read registered compute regions"))
		return
	}
	counts := map[string]int{}
	for _, node := range nodes {
		if node.Region != nil && *node.Region != "" && api.ValidComputeRegion(*node.Region) {
			counts[*node.Region]++
		}
	}
	regions := make([]api.ComputeRegionResponse, 0, len(counts))
	for id, count := range counts {
		regions = append(regions, api.ComputeRegionResponse{ID: id, ActiveNodes: count})
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].ID < regions[j].ID })
	writeJSON(w, http.StatusOK, regions)
}

func (s *server) validateComputeRegionAvailable(ctx context.Context, region string) *api.Problem {
	if region == "" {
		return nil
	}
	if !s.regionalComputeEnabled {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Regional compute unavailable", "regional compute placement has not been enabled by the operator")
	}
	nodes, err := s.store.ActiveComputeNodes(ctx)
	if err != nil {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Regions unavailable", "could not validate registered compute regions")
	}
	for _, node := range nodes {
		if state.ComputeRegionAllowsNode(region, node) {
			return nil
		}
	}
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Compute region unavailable", "compute_region must name a region with a registered active compute node; use GET /v1/regions")
}
