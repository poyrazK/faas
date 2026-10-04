// Package runtimequota defines the signed, durable regional capacity protocol.
// Verification authenticates authority; it does not establish scheduler drain.
package runtimequota

// adr: 487
import (
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrInvalid = errors.New("invalid runtime quota message")
	ErrTrust   = errors.New("runtime quota signer is not trusted")
	ErrBinding = errors.New("runtime quota audience conflicts")
)

// Binding identifies the original regional tenant and app, independent of an
// organization's creator account. CellGeneration is the directory registry
// generation retained by the allocation, not a regional database generation.
type Binding struct {
	CellID, Region                          string
	CellGeneration                          int64
	RegionalAccountID, RegionalOrgID        string
	RegionalAppID, RegionalAppSlug, APIBase string
}

type Grant struct {
	IssuerID, GlobalAppID, OwnerID, AllocationID string
	Generation                                   int64
	Binding                                      Binding
	Policy                                       api.GlobalRuntimeQuotaPolicy
	Concurrency, RolloutConcurrency              int
	IssuedAtMicros                               int64
}

// Retirement repeats the full original grant. A receiver must retain its
// tombstone even when retirement arrives before the grant itself.
type Retirement struct {
	Grant           Grant
	RetirementID    string
	RetiredAtMicros int64
}

// Drain must be created only by the regional fence/drain owner after durable
// admission closure and zero counted instances. A signature alone proves
// identity, not this operational condition.
type Drain struct {
	Retirement       Retirement
	AdmissionFrozen  bool
	CountedInstances int
}

func validID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func (b Binding) Validate() error {
	if !validID(b.CellID) || !validID(b.RegionalAccountID) || !validID(b.RegionalAppID) || (b.RegionalOrgID != "" && !validID(b.RegionalOrgID)) ||
		b.Region == "" || !api.ValidComputeRegion(b.Region) || b.CellGeneration < 1 || b.CellGeneration > api.GlobalRuntimeQuotaGenerationMax || !api.ValidGlobalAppSlug(b.RegionalAppSlug) || !api.ValidGlobalDirectoryAPIBase(b.APIBase) {
		return ErrInvalid
	}
	return nil
}

func (g Grant) Validate() error {
	p, ok := api.RuntimeQuotaPolicyForPlan(g.Policy.Plan)
	if g.validateIdentity() != nil || !ok || p != g.Policy {
		return ErrInvalid
	}
	return nil
}

func (g Grant) validateIdentity() error {
	p := g.Policy
	if !validID(g.IssuerID) || !validID(g.GlobalAppID) || !validID(g.OwnerID) || !validID(g.AllocationID) ||
		g.Generation < 1 || g.Generation > api.GlobalRuntimeQuotaGenerationMax || g.IssuedAtMicros < 1 || g.IssuedAtMicros > api.GlobalRuntimeQuotaGenerationMax ||
		g.Binding.Validate() != nil || !p.Plan.Valid() || p.Concurrency < 1 || p.PeakConcurrency < p.Concurrency || int64(p.PeakConcurrency) > api.GlobalRuntimeQuotaGenerationMax ||
		p.ID != api.RuntimeQuotaPolicyID(p.Plan, p.Concurrency, p.PeakConcurrency) || g.Concurrency < 1 || g.Concurrency > p.Concurrency || g.RolloutConcurrency < 0 || g.RolloutConcurrency > p.PeakConcurrency-p.Concurrency {
		return ErrInvalid
	}
	return nil
}

func (r Retirement) Validate() error {
	if r.Grant.validateIdentity() != nil || !validID(r.RetirementID) || r.RetiredAtMicros < 1 || r.RetiredAtMicros > api.GlobalRuntimeQuotaGenerationMax {
		return ErrInvalid
	}
	return nil
}

func (d Drain) Validate() error {
	if d.Retirement.Validate() != nil || !d.AdmissionFrozen || d.CountedInstances != 0 {
		return ErrInvalid
	}
	return nil
}
