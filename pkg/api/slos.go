package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
)

// CodeSLOInvalid and CodeSLOLimit are the stable problem codes for the
// ADR-747 SLO definition endpoints.
const (
	CodeSLOInvalid = "slo_invalid"
	CodeSLOLimit   = "slo_limit_reached"
)

var sloNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// CreateSLORequest is the POST /v1/apps/{slug}/slos body. ObjectivePct is a
// percentage with at most two decimals (99.9, 99.95); LatencyThresholdMS is
// required for the latency SLI and must be one of SLOLatencyThresholdsMS.
type CreateSLORequest struct {
	Name               string  `json:"name"`
	SLI                string  `json:"sli"`
	LatencyThresholdMS int     `json:"latency_threshold_ms,omitempty"`
	ObjectivePct       float64 `json:"objective_pct"`
	WindowDays         int     `json:"window_days"`
}

// SLOResponse is one customer-defined SLO definition.
type SLOResponse struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	SLI                string  `json:"sli"`
	LatencyThresholdMS int     `json:"latency_threshold_ms,omitempty"`
	ObjectivePct       float64 `json:"objective_pct"`
	WindowDays         int     `json:"window_days"`
	CreatedAt          string  `json:"created_at"`
	// Status is present on GET /v1/apps/{slug}/slos/{id} only.
	Status *SLOStatus `json:"status,omitempty"`
}

// SLOStatus is an SLO's error-budget position (ADR-747). Window figures come
// from the hourly rows meterd records; the burn rates are computed live.
// Percentages are null when the window or range saw no requests.
type SLOStatus struct {
	WindowStart        string   `json:"window_start"`
	HoursRecorded      int      `json:"hours_recorded"`
	HoursExpected      int      `json:"hours_expected"`
	Good               int64    `json:"good"`
	Total              int64    `json:"total"`
	AttainmentPct      *float64 `json:"attainment_pct"`
	BudgetRemainingPct *float64 `json:"budget_remaining_pct"`
	BurnRate1h         *float64 `json:"burn_rate_1h"`
	BurnRate6h         *float64 `json:"burn_rate_6h"`
	Source             string   `json:"source"`
}

// SLOObjectivePct renders basis points (9990) as a percentage (99.9).
func SLOObjectivePct(bp int) float64 { return float64(bp) / 100 }

// ValidateCreateSLO checks a create request against the closed sets the
// app_slos CHECKs enforce and returns the objective in basis points.
func ValidateCreateSLO(req CreateSLORequest) (int, *Problem) {
	if !sloNamePattern.MatchString(req.Name) {
		return 0, ErrSLOInvalid("name must match [a-z][a-z0-9_-]{0,62}")
	}
	switch req.SLI {
	case "availability":
		if req.LatencyThresholdMS != 0 {
			return 0, ErrSLOInvalid("latency_threshold_ms applies only to the latency SLI")
		}
	case "latency":
		if !slices.Contains(SLOLatencyThresholdsMS, req.LatencyThresholdMS) {
			return 0, ErrSLOInvalid(fmt.Sprintf("latency_threshold_ms must be one of %v", SLOLatencyThresholdsMS))
		}
	default:
		return 0, ErrSLOInvalid("sli must be availability or latency")
	}
	bp := math.Round(req.ObjectivePct * 100)
	if math.IsNaN(req.ObjectivePct) || math.Abs(bp-req.ObjectivePct*100) > 1e-6 || bp < SLOObjectiveMinBP || bp > SLOObjectiveMaxBP {
		return 0, ErrSLOInvalid(fmt.Sprintf("objective_pct must be between %.2f and %.2f with at most two decimals", SLOObjectivePct(SLOObjectiveMinBP), SLOObjectivePct(SLOObjectiveMaxBP)))
	}
	if !slices.Contains(SLOWindowDays, req.WindowDays) {
		return 0, ErrSLOInvalid(fmt.Sprintf("window_days must be one of %v", SLOWindowDays))
	}
	return int(bp), nil
}

// ErrSLOInvalid is a 400 for a malformed SLO definition.
func ErrSLOInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSLOInvalid, "Invalid SLO", reason).
		WithDocs(docsBase + "/slos")
}

// ErrSLOLimitReached is a 422 naming the per-app cap and the observed count.
func ErrSLOLimitReached(observed int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeSLOLimit, "SLO limit reached",
		fmt.Sprintf("an app can define at most %d SLOs; delete one to add another", MaxSLOsPerApp)).
		WithLimit(int64(MaxSLOsPerApp), int64(observed)).
		WithDocs(docsBase + "/slos")
}

// ListSLOs returns an app's customer-defined SLOs (ADR-747), name-ordered.
func (c *Client) ListSLOs(ctx context.Context, slug string) ([]SLOResponse, error) {
	var out []SLOResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/slos", nil, &out)
}

// CreateSLO defines an SLO on an app (ADR-747).
func (c *Client) CreateSLO(ctx context.Context, slug string, req CreateSLORequest) (SLOResponse, error) {
	var out SLOResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/slos", req, &out)
}

// GetSLO fetches one SLO by id (ADR-747).
func (c *Client) GetSLO(ctx context.Context, slug, id string) (SLOResponse, error) {
	var out SLOResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/slos/"+id, nil, &out)
}

// DeleteSLO removes one SLO (ADR-747).
func (c *Client) DeleteSLO(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/slos/"+id, nil, nil)
}
