package state

import (
	"encoding/json"
	"errors"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateProfileMixSnapshot(s *api.ProfileRequestMixSnapshot, a, b api.ProfileQuery) error {
	if s == nil {
		return nil
	}
	body, err := json.Marshal(s)
	if err != nil || len(body) > api.ProfileRequestMixSnapshotMaxBytes || s.CapturedAt.IsZero() || len(s.Warnings) > 8 || len(s.Reason) > 1024 {
		return errors.New("invalid request-mix snapshot or storage budget")
	}
	if s.Status != "captured" && s.Status != "partial" && s.Status != "unavailable" {
		return errors.New("invalid request-mix capture status")
	}
	if s.Status == "captured" && (s.Baseline == nil || s.Candidate == nil) || s.Status == "unavailable" && (s.Baseline != nil || s.Candidate != nil) {
		return errors.New("request-mix capture status does not match its windows")
	}
	for _, d := range []*float64{s.RouteDifference, s.StatusDifference} {
		if d != nil && (math.IsNaN(*d) || math.IsInf(*d, 0) || *d < 0 || *d > 100+1e-9) {
			return errors.New("invalid request-mix difference")
		}
	}
	for _, w := range s.Warnings {
		if !investigationText(w, 1024) {
			return errors.New("invalid request-mix warning")
		}
	}
	for i, w := range []*api.ProfileRequestMixWindow{s.Baseline, s.Candidate} {
		if w == nil {
			continue
		}
		query := a
		if i == 1 {
			query = b
		}
		if !sameInvestigationSelection(w.Query, query) || w.Total < 0 || len(w.Routes) > api.ProfileRequestMixSnapshotMaxRoutes || len(w.Statuses) > 5 {
			return errors.New("invalid request-mix window")
		}
		for _, groups := range [][]api.ProfileRequestMixGroup{w.Routes, w.Statuses} {
			var total int64
			for _, g := range groups {
				if !investigationText(g.Label, 1024) || !investigationText(g.Method, 16) || g.Requests < 0 || g.Requests > w.Total-total {
					return errors.New("invalid request-mix counts")
				}
				total += g.Requests
			}
		}
	}
	if s.Complete && (s.Status != "captured" || s.Baseline == nil || s.Candidate == nil || s.Baseline.Truncated || s.Candidate.Truncated) {
		return errors.New("invalid request-mix completeness")
	}
	return nil
}
