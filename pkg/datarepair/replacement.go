package datarepair

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Replacement is digest-bound lineage and a comparison of two frozen candidate
// sets. It is review context, never rehearsal evidence or approval.
type Replacement struct {
	ParentPlanID       string `json:"parent_plan_id"`
	ParentPlanDigest   string `json:"parent_plan_digest"`
	PreviousCandidates int    `json:"previous_candidates"`
	RetainedCandidates int    `json:"retained_candidates"`
	RemovedCandidates  int    `json:"removed_candidates"`
	PreviousExcluded   int    `json:"previous_excluded"`
}

func (r Replacement) Valid(p Plan) bool {
	if !validID(p.ID) || !validID(p.SourceOperationID) || p.Scope.Validate() != nil || !digestPattern.MatchString(p.Digest) || p.ExpiresAt.IsZero() ||
		!validID(r.ParentPlanID) || r.ParentPlanID == p.ID || r.ParentPlanID == p.SourceOperationID || !digestPattern.MatchString(r.ParentPlanDigest) ||
		r.PreviousCandidates < 1 || r.PreviousCandidates > api.DataRepairMaxRows || r.PreviousExcluded < 0 ||
		r.PreviousExcluded > api.DataRepairMaxRows-r.PreviousCandidates || r.RetainedCandidates < 0 || r.RetainedCandidates > r.PreviousCandidates ||
		r.RemovedCandidates < 0 || r.RemovedCandidates > r.PreviousCandidates || r.RetainedCandidates+r.RemovedCandidates != r.PreviousCandidates ||
		r.RetainedCandidates != p.Candidates || p.Conflicts < 0 || p.Conflicts > api.DataRepairMaxRows || p.Unavailable < 0 || p.Unavailable > api.DataRepairMaxRows ||
		p.Conflicts+p.Unavailable != r.PreviousExcluded+r.RemovedCandidates {
		return false
	}
	if p.PayloadPurgedAt != nil {
		return len(p.Items) == 0
	}
	if len(p.Items) != p.Candidates+p.Conflicts+p.Unavailable {
		return false
	}
	previous, retained, removed, excluded := 0, 0, 0, 0
	conflicts, unavailable := 0, 0
	seen := map[string]bool{}
	for _, item := range p.Items {
		if !validID(item.RowIdentity) || seen[item.RowIdentity] {
			return false
		}
		seen[item.RowIdentity] = true
		switch item.Status {
		case "candidate":
		case "conflict":
			conflicts++
		case "unavailable":
			unavailable++
		default:
			return false
		}
		switch item.PreviousStatus {
		case "candidate":
			previous++
			if item.Status == "candidate" {
				retained++
			} else {
				removed++
			}
		case "conflict", "unavailable":
			if item.Status != item.PreviousStatus {
				return false
			}
			excluded++
		default:
			return false
		}
	}
	return previous == r.PreviousCandidates && retained == r.RetainedCandidates && removed == r.RemovedCandidates && excluded == r.PreviousExcluded &&
		conflicts == p.Conflicts && unavailable == p.Unavailable
}

// Replace creates one new immutable selection and fences its parent in the same
// application transaction. The row lock serializes it with apply through COMMIT;
// a committed correction can never be replaced. Same-ID replay returns the
// original child, including its fixed expiry and any later receipt.
func (e *Engine) Replace(ctx context.Context, s Scope, parent, digest, id string) (Plan, error) {
	if err := e.ready(s); err != nil {
		return Plan{}, err
	}
	if !validID(parent) || !validID(id) || parent == id || !digestPattern.MatchString(digest) {
		return Plan{}, ErrInvalid
	}
	set, err := configuredChecks(e.CheckSQL, e.Checks)
	if err != nil {
		return Plan{}, err
	}
	var manifest any
	if set.checks != nil {
		manifest, _ = json.Marshal(set.descriptors)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	p, err := readPlan(ctx, e.Pool, `SELECT gregale_repair.replace_plan($1::uuid,$2::jsonb,$3,$4::uuid,$5,$6::jsonb)`, parent, scopeJSON(s), digest, id, set.digest, manifest)
	if err != nil {
		return Plan{}, err
	}
	if p.ID != id || p.Scope != s || p.Replacement == nil || p.Replacement.ParentPlanID != parent || p.Replacement.ParentPlanDigest != digest || !p.Replacement.Valid(p) {
		return Plan{}, ErrUnavailable
	}
	return p, nil
}
