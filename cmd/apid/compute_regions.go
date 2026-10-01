package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listComputeRegions(w http.ResponseWriter, r *http.Request, _ state.Account) {
	regions, err := s.store.ListComputeRegions(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read registered compute regions"))
		return
	}
	nodes, err := s.store.ListAllComputeNodes(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read compute region availability"))
		return
	}
	out := make([]api.ComputeRegionResponse, 0, len(regions))
	now := time.Now()
	for _, region := range regions {
		out = append(out, computeRegionResponse(region, nodes, now))
	}
	writeJSON(w, http.StatusOK, out)
}

func computeRegionResponse(region state.ComputeRegion, nodes []state.ComputeNode, now time.Time) api.ComputeRegionResponse {
	out := api.ComputeRegionResponse{ID: region.ID, DisplayName: region.DisplayName, AcceptingNewApps: region.AcceptingNewApps,
		Availability: "unavailable", ObservedHealth: "unavailable"}
	observed := 0
	for _, node := range nodes {
		if !state.ComputeRegionAllowsNode(region.ID, node) {
			continue
		}
		out.RegisteredNodes++
		if node.Active {
			out.ActiveNodes++
		}
		if !state.ComputeNodePlacementEligible(node) {
			continue
		}
		out.EligibleNodes++
		if node.LastHeartbeatAt.IsZero() {
			continue
		}
		observed++
		age := now.Sub(node.LastHeartbeatAt)
		if age >= 0 && age <= state.DefaultHeartbeatStaleness {
			out.HealthyNodes++
		}
	}
	if out.EligibleNodes > 0 {
		out.Availability = "available"
		switch {
		case out.HealthyNodes == out.EligibleNodes:
			out.ObservedHealth = "healthy"
		case out.HealthyNodes > 0:
			out.ObservedHealth = "degraded"
		case observed == 0:
			out.ObservedHealth = "unknown"
		}
	}
	return out
}

func (s *server) putComputeRegion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	var req api.UpdateComputeRegionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || id == "" || !api.ValidComputeRegion(id) ||
		!api.ValidComputeRegionDisplayName(req.DisplayName) || req.AcceptingNewApps == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid compute region policy",
			"Use a canonical region id, a display_name of 1–128 characters without surrounding whitespace or control characters, and an explicit accepting_new_apps boolean."))
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid JSON", "Send exactly one region policy object."))
		return
	}
	// Read observations before mutating so an unavailable reader cannot leave a
	// successful intent hidden behind a follow-up failure.
	nodes, err := s.store.ListAllComputeNodes(r.Context())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read compute region availability"))
		return
	}
	region, err := s.store.UpsertComputeRegion(r.Context(), state.ComputeRegion{ID: id, DisplayName: req.DisplayName, AcceptingNewApps: *req.AcceptingNewApps})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update compute region policy"))
		return
	}
	s.audit.Emit(r.Context(), "compute_region.policy_updated", &acct.ID, map[string]any{
		"compute_region": id, "display_name": region.DisplayName, "accepting_new_apps": region.AcceptingNewApps,
	})
	writeJSON(w, http.StatusOK, computeRegionResponse(region, nodes, time.Now()))
}

func (s *server) validateComputeRegionAvailable(ctx context.Context, id string) *api.Problem {
	return state.ValidateNewComputeRegion(ctx, s.store, s.regionalComputeEnabled, id)
}
