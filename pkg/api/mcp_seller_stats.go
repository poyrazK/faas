package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// MCPSellerStatsResponse is GET /v1/apps/{slug}/mcp-stats
// (ADR-20261126082046): how a sold MCP server's customers, tools and
// revenue are doing over UTC calendar months.
type MCPSellerStatsResponse struct {
	App         string    `json:"app"`
	GeneratedAt time.Time `json:"generated_at"`
	// Months lists each month of the window, oldest first; the last is the
	// current month to date.
	Months []MCPSellerStatsMonth `json:"months"`
	// Tools covers the whole window; estimated revenue is this month's.
	Tools []MCPSellerStatsTool `json:"tools"`
	// NearLimit lists open customers at 80% or more of their plan's monthly
	// units this month, most used first.
	NearLimit []MCPSellerNearLimit `json:"near_limit"`
	// Customers counts customers now.
	Customers MCPSellerCustomerTotals `json:"customers"`
	// EstimateComplete is false when this month's estimate priced only the
	// MCPSellerStatsMaxQuotes customers with the most units.
	EstimateComplete bool `json:"estimate_complete"`
}

// MCPSellerStatsMonth is one UTC calendar month.
type MCPSellerStatsMonth struct {
	// Month is YYYY-MM.
	Month   string `json:"month"`
	Current bool   `json:"current"`
	// SignedUp counts customers created in the month; BySource splits them
	// into oauth, signup and owner.
	SignedUp         int64            `json:"signed_up"`
	SignedUpBySource map[string]int64 `json:"signed_up_by_source"`
	// Active counts customers with billable units in the month.
	Active int64 `json:"active"`
	// Paying counts customers whose month amounts to more than zero.
	Paying int64 `json:"paying"`
	// Closed counts customers revoked in the month.
	Closed   int64                  `json:"closed"`
	Requests int64                  `json:"requests"`
	Units    int64                  `json:"units"`
	Revenue  []MCPSellerStatsAmount `json:"revenue"`
}

// MCPSellerStatsAmount is revenue in one currency. Billed sums finalized
// statements before credits; Estimated prices the current month so far.
type MCPSellerStatsAmount struct {
	Currency            string `json:"currency"`
	BilledMillicents    int64  `json:"billed_millicents"`
	EstimatedMillicents int64  `json:"estimated_millicents"`
}

// MCPSellerStatsTool is one MCP tool's usage over the window.
type MCPSellerStatsTool struct {
	Tool      string `json:"tool"`
	Units     int64  `json:"units"`
	Customers int64  `json:"customers"`
	// Revenue is this month's estimate for the tool, per currency.
	Revenue []MCPSellerStatsAmount `json:"revenue,omitempty"`
}

// MCPSellerNearLimit is a customer close to its plan's monthly units.
type MCPSellerNearLimit struct {
	ConsumerID       string `json:"consumer_id"`
	Name             string `json:"name"`
	Plan             string `json:"plan"`
	Units            int64  `json:"units"`
	MaxUnitsPerMonth int64  `json:"max_units_per_month"`
	Percent          int64  `json:"percent"`
}

// MCPSellerCustomerTotals counts an app's customers now.
type MCPSellerCustomerTotals struct {
	Open   int64 `json:"open"`
	Closed int64 `json:"closed"`
}

// Seller stats sources (MCPSellerStatsMonth.SignedUpBySource).
const (
	MCPSellerSourceOAuth  = "oauth"
	MCPSellerSourceSignup = "signup"
	MCPSellerSourceOwner  = "owner"
)

// GetMCPSellerStats reads an app's seller stats over the last months
// calendar months (1 = this month to date).
func (c *Client) GetMCPSellerStats(ctx context.Context, slug string, months int) (MCPSellerStatsResponse, error) {
	var out MCPSellerStatsResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/mcp-stats"
	if months > 0 {
		path += "?months=" + strconv.Itoa(months)
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}
