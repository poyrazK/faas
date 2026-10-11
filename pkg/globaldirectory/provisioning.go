package globaldirectory

// adr: 797
import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ProvisionPlanned   = "planned"
	ProvisionAttached  = "attached"
	ProvisionCancelled = "cancelled"
)

type AppProvisionSpec struct {
	VerifiedCell Cell
	Region       string
	AccountID    string
	Request      json.RawMessage
}

type AppProvision struct {
	AppProvisionSpec
	ID          string
	GlobalAppID string
	Cell        Cell
	Status      string
}

type AppProvisionStore interface {
	PlanAppProvisioning(context.Context, string, string, []AppProvisionSpec) ([]AppProvision, error)
	ListAppProvisioning(context.Context, string, string) ([]AppProvision, error)
	AttachProvisionTarget(context.Context, string, string, string, Binding) (Target, error)
}

func canonicalProvisionSpecs(specs []AppProvisionSpec) ([]AppProvisionSpec, error) {
	if len(specs) == 0 {
		return nil, ErrConflict
	}
	if len(specs) > api.GlobalImageReleaseTargetsMax {
		return nil, &ReleaseLimitError{Kind: "targets", Limit: api.GlobalImageReleaseTargetsMax, Observed: int64(len(specs))}
	}
	out := make([]AppProvisionSpec, len(specs))
	seen := make(map[string]bool)
	for i, s := range specs {
		var req api.CreateAppRequest
		if !ValidID(s.AccountID) || s.Region == "" || !api.ValidComputeRegion(s.Region) || seen[s.Region] || len(s.Request) > api.GlobalDirectoryRequestMaxBytes || json.Unmarshal(s.Request, &req) != nil || !ValidSlug(req.Slug) || req.ComputeRegion != s.Region {
			return nil, ErrConflict
		}
		seen[s.Region] = true
		body, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		s.Request = body
		out[i] = s
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region < out[j].Region })
	return out, nil
}

func (p AppProvision) matches(s AppProvisionSpec) bool {
	return p.Region == s.Region && p.AccountID == s.AccountID && bytes.Equal(p.Request, s.Request)
}

func (p AppProvision) bindingMatches(b Binding) bool {
	var req api.CreateAppRequest
	return json.Unmarshal(p.Request, &req) == nil && b.CellID == p.Cell.ID && b.Region == p.Region && b.CellGeneration == p.Cell.Generation && b.RegionalAccountID == p.AccountID && b.RegionalOrgID == "" && b.RegionalAppSlug == req.Slug
}

func cloneAppProvision(p AppProvision) AppProvision {
	p.Request = append([]byte(nil), p.Request...)
	return p
}
