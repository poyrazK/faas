package api

// adr: 486
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
)

type GlobalRuntimeQuotaShare struct {
	Region      string `json:"region"`
	Concurrency int    `json:"concurrency"`
}

type PutGlobalRuntimeQuotaRequest struct {
	Shares        []GlobalRuntimeQuotaShare `json:"shares"`
	RolloutRegion string                    `json:"rollout_region,omitempty"`
}

type GlobalRuntimeQuotaPolicy struct {
	ID              string `json:"id"`
	Plan            Plan   `json:"plan"`
	Concurrency     int    `json:"concurrency"`
	PeakConcurrency int    `json:"peak_concurrency"`
}

// RuntimeQuotaPolicyForPlan is shared by directory allocation and regional
// signature verification. Limits remain exclusively in LimitsFor.
func RuntimeQuotaPolicyForPlan(plan Plan) (GlobalRuntimeQuotaPolicy, bool) {
	limits, ok := LimitsFor(plan)
	if !ok {
		return GlobalRuntimeQuotaPolicy{}, false
	}
	p := GlobalRuntimeQuotaPolicy{Plan: plan, Concurrency: limits.MaxConcurrency, PeakConcurrency: limits.MaxConcurrency + RolloutConcurrencyGrant}
	p.ID = RuntimeQuotaPolicyID(p.Plan, p.Concurrency, p.PeakConcurrency)
	return p, true
}

// RuntimeQuotaPolicyID also identifies historical snapshots during retirement;
// issuing new capacity still requires equality with RuntimeQuotaPolicyForPlan.
func RuntimeQuotaPolicyID(plan Plan, concurrency, peakConcurrency int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("gregale-runtime-quota-v1:%s:%d:%d", plan, concurrency, peakConcurrency)))
	return hex.EncodeToString(digest[:])
}

type GlobalRuntimeQuotaTargetResponse struct {
	CellID             string     `json:"cell_id"`
	Region             string     `json:"region"`
	CellGeneration     int64      `json:"cell_generation"`
	RegionalAccountID  string     `json:"regional_account_id"`
	RegionalOrgID      string     `json:"regional_org_id,omitempty"`
	RegionalAppID      string     `json:"regional_app_id"`
	RegionalAppSlug    string     `json:"regional_app_slug"`
	Concurrency        int        `json:"concurrency"`
	RolloutConcurrency int        `json:"rollout_concurrency"`
	IssuedAt           *time.Time `json:"issued_at"`
	ReleasedAt         *time.Time `json:"released_at"`
}

// Reservation status describes directory accounting, not scheduler readiness.
type GlobalRuntimeQuotaResponse struct {
	GlobalAppID  string                             `json:"global_app_id"`
	AllocationID string                             `json:"allocation_id"`
	Generation   int64                              `json:"generation"`
	Policy       GlobalRuntimeQuotaPolicy           `json:"policy"`
	Status       string                             `json:"status"`
	CreatedAt    time.Time                          `json:"created_at"`
	RetirementID *string                            `json:"retirement_id"`
	RetiredAt    *time.Time                         `json:"retired_at"`
	Targets      []GlobalRuntimeQuotaTargetResponse `json:"targets"`
}

func (c *Client) PutGlobalRuntimeQuota(ctx context.Context, slug, allocationID string, req PutGlobalRuntimeQuotaRequest) (GlobalRuntimeQuotaResponse, error) {
	var out GlobalRuntimeQuotaResponse
	path, err := globalRuntimeQuotaPath(slug, allocationID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "PUT", path, req, &out, allocationID)
}

func (c *Client) GetGlobalRuntimeQuota(ctx context.Context, slug, allocationID string) (GlobalRuntimeQuotaResponse, error) {
	var out GlobalRuntimeQuotaResponse
	path, err := globalRuntimeQuotaPath(slug, allocationID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "GET", path, nil, &out, "")
}

func (c *Client) RetireGlobalRuntimeQuota(ctx context.Context, slug, allocationID string) (GlobalRuntimeQuotaResponse, error) {
	var out GlobalRuntimeQuotaResponse
	path, err := globalRuntimeQuotaPath(slug, allocationID)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "DELETE", path, nil, &out, "")
}

func globalRuntimeQuotaPath(slug, allocationID string) (string, error) {
	id, err := uuid.Parse(allocationID)
	if err != nil || id == uuid.Nil || id.String() != allocationID {
		return "", fmt.Errorf("allocation_id must be a canonical nonzero UUID")
	}
	return "/v1/global-apps/" + url.PathEscape(slug) + "/runtime-quotas/" + url.PathEscape(allocationID), nil
}
