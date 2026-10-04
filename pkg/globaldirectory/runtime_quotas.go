package globaldirectory

// adr: 486
import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrRuntimeQuotaLimit    = errors.New("global runtime quota exhausted")
	ErrRuntimeQuotaRetired  = errors.New("global runtime quota retired")
	ErrRuntimeQuotaIdentity = errors.New("global runtime quota acknowledgement identity conflicts")
)

// RuntimeQuotaPolicy is derived exclusively from pkg/api/limits.go. Its digest
// identifies an immutable entitlement snapshot, including rollout headroom.
type RuntimeQuotaPolicy = api.GlobalRuntimeQuotaPolicy

func runtimeQuotaPolicy(plan api.Plan) (RuntimeQuotaPolicy, error) {
	p, ok := api.RuntimeQuotaPolicyForPlan(plan)
	if !ok {
		return RuntimeQuotaPolicy{}, ErrConflict
	}
	return p, nil
}

type RuntimeQuotaShare struct {
	Region      string
	Concurrency int
}

type RuntimeQuotaRequest struct {
	OwnerID, ActorID, Slug, AllocationID string
	ExpectedPlan                         api.Plan
	Shares                               []RuntimeQuotaShare
	RolloutRegion                        string
}

type RuntimeQuotaTarget struct {
	Target
	Concurrency, RolloutConcurrency int
	IssuedAt, ReleasedAt            *time.Time
}

type RuntimeQuotaAllocation struct {
	GlobalAppID, OwnerID, AllocationID string
	Generation                         int64
	Policy                             RuntimeQuotaPolicy
	CreatedAt                          time.Time
	RetirementID                       string
	RetiredAt                          *time.Time
	Targets                            []RuntimeQuotaTarget
}

// RuntimeQuotaRetirement is an internal trusted result, never a customer claim.
// The regional protocol must verify a durable admission fence and zero counted
// instances before constructing it. Expiry, outage and detach are not proof.
type RuntimeQuotaRetirement struct {
	AllocationID, RetirementID string
	Generation                 int64
	Binding                    Binding
	APIBase                    string
	AdmissionFrozen            bool
	CountedInstances           int
}

type RuntimeQuotaStore interface {
	ReserveRuntimeQuota(context.Context, RuntimeQuotaRequest) (RuntimeQuotaAllocation, error)
	RuntimeQuota(context.Context, string, string, string) (RuntimeQuotaAllocation, error)
	IssueRuntimeQuotaTarget(context.Context, string, string, string, string) (RuntimeQuotaTarget, error)
	RetireRuntimeQuota(context.Context, string, string, string, string) (RuntimeQuotaAllocation, error)
	ConfirmRuntimeQuotaRetirement(context.Context, string, string, RuntimeQuotaRetirement) error
}

type RuntimeQuotaLimitError struct {
	Kind            string
	Limit, Observed int64
}

func (e *RuntimeQuotaLimitError) Error() string { return ErrRuntimeQuotaLimit.Error() }
func (e *RuntimeQuotaLimitError) Unwrap() error { return ErrRuntimeQuotaLimit }

func runtimeQuotaLimitFromDetail(detail string) error {
	fields := strings.Split(detail, ":")
	if len(fields) != 3 || (fields[0] != "concurrency" && fields[0] != "peak") {
		return ErrConflict
	}
	limit, e1 := strconv.ParseInt(fields[1], 10, 64)
	observed, e2 := strconv.ParseInt(fields[2], 10, 64)
	if e1 != nil || e2 != nil || limit < 1 || observed <= limit {
		return ErrConflict
	}
	return &RuntimeQuotaLimitError{Kind: fields[0], Limit: limit, Observed: observed}
}

func canonicalQuotaRequest(req RuntimeQuotaRequest) (RuntimeQuotaRequest, RuntimeQuotaPolicy, error) {
	p, err := runtimeQuotaPolicy(req.ExpectedPlan)
	if err != nil || !validOwnerID(req.OwnerID) || !validOwnerID(req.ActorID) || !ValidSlug(req.Slug) || !ValidID(req.AllocationID) || len(req.Shares) == 0 || len(req.Shares) > api.GlobalRuntimeQuotaTargetsMax {
		return req, p, ErrConflict
	}
	req.Shares = append([]RuntimeQuotaShare(nil), req.Shares...)
	sort.Slice(req.Shares, func(i, j int) bool { return req.Shares[i].Region < req.Shares[j].Region })
	var total int64
	for i, share := range req.Shares {
		if !api.ValidComputeRegion(share.Region) || share.Region == "" || share.Concurrency < 1 || (i > 0 && req.Shares[i-1].Region == share.Region) {
			return req, p, ErrConflict
		}
		if share.Concurrency > p.Concurrency {
			return req, p, &RuntimeQuotaLimitError{Kind: "concurrency", Limit: int64(p.Concurrency), Observed: int64(share.Concurrency)}
		}
		total += int64(share.Concurrency)
	}
	if total > int64(p.Concurrency) {
		return req, p, &RuntimeQuotaLimitError{Kind: "concurrency", Limit: int64(p.Concurrency), Observed: total}
	}
	if req.RolloutRegion == "" {
		req.RolloutRegion = req.Shares[0].Region
	}
	found := false
	for _, share := range req.Shares {
		found = found || share.Region == req.RolloutRegion
	}
	if !found {
		return req, p, ErrConflict
	}
	return req, p, nil
}

func quotaInputMatches(allocation RuntimeQuotaAllocation, req RuntimeQuotaRequest, policy RuntimeQuotaPolicy) bool {
	if allocation.Policy != policy || len(allocation.Targets) != len(req.Shares) {
		return false
	}
	for i, target := range allocation.Targets {
		rollout := 0
		if req.Shares[i].Region == req.RolloutRegion {
			rollout = api.RolloutConcurrencyGrant
		}
		if target.Region != req.Shares[i].Region || target.Concurrency != req.Shares[i].Concurrency || target.RolloutConcurrency != rollout {
			return false
		}
	}
	return true
}

func copyQuota(allocation RuntimeQuotaAllocation) RuntimeQuotaAllocation {
	copyTime := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		v := *t
		return &v
	}
	allocation.RetiredAt = copyTime(allocation.RetiredAt)
	allocation.Targets = append([]RuntimeQuotaTarget(nil), allocation.Targets...)
	for i := range allocation.Targets {
		allocation.Targets[i].IssuedAt = copyTime(allocation.Targets[i].IssuedAt)
		allocation.Targets[i].ReleasedAt = copyTime(allocation.Targets[i].ReleasedAt)
	}
	return allocation
}

func quotaRetirementMatches(allocation RuntimeQuotaAllocation, target RuntimeQuotaTarget, ack RuntimeQuotaRetirement) bool {
	return allocation.RetirementID != "" && allocation.RetirementID == ack.RetirementID && allocation.AllocationID == ack.AllocationID && allocation.Generation == ack.Generation &&
		target.Binding == ack.Binding && target.APIBase == ack.APIBase && target.IssuedAt != nil && ack.AdmissionFrozen && ack.CountedInstances == 0
}
