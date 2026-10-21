// adr: 566 — gross compute authority is distinct from delayed cost reports.
package financial

import (
	"errors"
	"github.com/google/uuid"
	"math/big"
	"time"
)

var (
	ErrComputeBudget      = errors.New("financial: compute budget exhausted")
	ErrComputeFence       = errors.New("financial: compute lease fence changed")
	ErrComputeUnavailable = errors.New("financial: compute authority unavailable")
)

// StrictAppCompute caps gross compute for one app, independently of allowance.
func StrictAppCompute(p BudgetSpec) bool {
	return p.Mode == "strict" && p.Scope.Kind == "app" && p.Basis == "gross_usage" && p.Action == "suspend_workloads" && len(p.Meters) == 1 && p.Meters[0] == "compute" && p.DrainSeconds == 0 && len(p.NotifyMillicents) == 0
}

// StrictAccountCompute shares one allowance and paid compute ceiling across
// every app in the account. Unsupported producers must be fenced at activation.
func StrictAccountCompute(p BudgetSpec) bool {
	return p.Mode == "strict" && p.Scope.Kind == "account" && p.Basis == "net_usage" && p.Action == "suspend_workloads" && (len(p.Meters) == 1 && p.Meters[0] == "compute" || len(p.Meters) == 2 && p.Meters[0] == "compute" && p.Meters[1] == "egress") && p.DrainSeconds == 0 && len(p.NotifyMillicents) == 0
}

func StrictJobCompute(p BudgetSpec) bool {
	return p.Mode == "strict" && p.Scope.Kind == "job" && p.Basis == "gross_usage" && p.Action == "suspend_workloads" && len(p.Meters) == 1 && p.Meters[0] == "compute" && p.DrainSeconds == 0 && len(p.NotifyMillicents) == 0
}

func StrictCompute(p BudgetSpec) bool {
	return StrictAppCompute(p) || StrictAccountCompute(p) || StrictJobCompute(p)
}

// ComputeBalance is a conservative admission ledger, not an invoice. Committed
// includes startup and rounds upwards; refunds/corrections never erase it.
type ComputeBalance struct {
	Egress              *EgressBalance `json:"egress,omitempty"`
	Initialized         bool           `json:"initialized"`
	AppID               string         `json:"app_id,omitempty"`
	JobID               string         `json:"job_id,omitempty"`
	AccountID           string         `json:"account_id"`
	ScopeKind           string         `json:"scope_kind"`
	Basis               string         `json:"basis"`
	AllowanceGranted    int64          `json:"allowance_granted_mb_seconds"`
	AllowanceCommitted  int64          `json:"allowance_committed_mb_seconds"`
	AllowanceReserved   int64          `json:"allowance_reserved_mb_seconds"`
	AllowanceRemaining  int64          `json:"allowance_remaining_mb_seconds"`
	PeriodStart         time.Time      `json:"period_start"`
	PeriodEnd           time.Time      `json:"period_end"`
	CommittedMillicents int64          `json:"committed_millicents"`
	ReservedMillicents  int64          `json:"reserved_millicents"`
	RemainingMillicents int64          `json:"remaining_millicents"`
	UnconfirmedLeases   int            `json:"unconfirmed_leases"`
	Held                bool           `json:"held"`
	ObservedAt          time.Time      `json:"observed_at"`
}

type ComputeBook struct {
	Egress      EgressBook `json:"egress"`
	AccountID   string     `json:"account_id"`
	AppID       string     `json:"app_id"`
	JobID       string     `json:"job_id,omitempty"`
	PeriodStart time.Time  `json:"period_start"`
	PeriodEnd   time.Time  `json:"period_end"`
	Committed   int64      `json:"committed"`
	Reserved    int64      `json:"reserved"`
	// Quantity fields apply only to the shared account book, where both
	// resource IDs are empty.
	QuantityCommitted int64 `json:"quantity_committed"`
	QuantityReserved  int64 `json:"quantity_reserved"`
	AllowanceGranted  int64 `json:"allowance_granted"`
}

// ComputeLease pins a single physical attempt to a node incarnation. An expired
// lease remains funded until its owner confirms physical retirement. Strict
// billing can consume only [StartedAt, FundedUntil), even after a daemon crash.
type ComputeLease struct {
	EgressFunded        bool       `json:"egress_funded"`
	EgressGranted       int64      `json:"egress_granted_bytes"`
	EgressGrant         int64      `json:"egress_grant_bytes"`
	EgressSequence      int64      `json:"egress_sequence"`
	ID                  string     `json:"id"`
	Fence               string     `json:"fence"`
	RequestID           string     `json:"request_id"`
	RequestOperation    string     `json:"request_operation"`
	RequestEgressRefill bool       `json:"request_egress_refill"`
	RequestRevision     int64      `json:"request_revision"`
	Revision            int64      `json:"revision"`
	AccountID           string     `json:"account_id"`
	AppID               string     `json:"app_id"`
	JobID               string     `json:"job_id,omitempty"`
	JobRunID            string     `json:"job_run_id,omitempty"`
	JobTaskIndex        int        `json:"job_task_index,omitempty"`
	InstanceID          string     `json:"instance_id"`
	NodeID              string     `json:"node_id"`
	Incarnation         string     `json:"incarnation"`
	Strict              bool       `json:"strict"`
	AccountFunded       bool       `json:"account_funded"`
	QuantityCommitted   int64      `json:"quantity_committed"`
	QuantityReserved    int64      `json:"quantity_reserved"`
	Denied              bool       `json:"denied"`
	AttemptID           string     `json:"attempt_id"`
	Fenced              bool       `json:"fenced"`
	DenialReason        string     `json:"denial_reason,omitempty"`
	BillableMB          int        `json:"billable_mb"`
	RateMillicents      int64      `json:"rate_millicents"`
	RateQuantity        int64      `json:"rate_quantity"`
	PeriodStart         time.Time  `json:"period_start"`
	StartedAt           time.Time  `json:"started_at"`
	GrantedAt           time.Time  `json:"granted_at"`
	RunUntil            time.Time  `json:"run_until"`
	FundedUntil         time.Time  `json:"funded_until"`
	Committed           int64      `json:"committed"`
	Reserved            int64      `json:"reserved"`
	SettledAt           *time.Time `json:"settled_at,omitempty"`
}

type ComputeRequest struct {
	EgressRefill                                      bool // native owner requests one additional bounded byte credit
	AttemptID                                         string
	Fenced                                            bool
	LeaseID, Fence, RequestID                         string
	AccountID, AppID, InstanceID, NodeID, Incarnation string
	BillableMB                                        int
	Revision                                          int64
	JobID, JobRunID                                   string
	JobTaskIndex                                      int
}

// BindComputeRequest derives one stable financial identity from the scheduler's
// persisted wake ID or command lease. A delayed replay cannot mint a new VM
// lease after cancellation, settlement, policy edits or vmmd restart.
func BindComputeRequest(r ComputeRequest) (ComputeRequest, error) {
	if uuid.Validate(r.AttemptID) != nil {
		return r, ErrComputeUnavailable
	}
	resource := r.AppID
	if r.JobRunID != "" {
		resource = "job:" + ComputeJobAttemptID(r.JobRunID, r.JobTaskIndex, "")
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("financial-compute-attempt\n"+r.AccountID+"\n"+resource+"\n"+r.InstanceID+"\n"+r.AttemptID))
	r.LeaseID = id.String()
	r.Fence = uuid.NewSHA1(id, []byte("fence")).String()
	r.RequestID = uuid.NewSHA1(id, []byte("acquire")).String()
	return r, nil
}

// ComputeCharge ceilings each funded UTC minute and retains one millicent per
// minute for independent allowance/net allocation rounding. Price activations
// and usage evidence are minute-granular. Integer arithmetic also covers
// sub-second startup; no shared allowance is consumed by a scoped budget.
func ComputeCharge(mb int, from, until time.Time, rate, quantity int64) (int64, error) {
	if mb <= 0 || from.IsZero() || until.Before(from) || rate < 0 || quantity <= 0 {
		return 0, ErrInvalid
	}
	if rate == 0 || from.Equal(until) {
		return 0, nil
	}
	chunk := func(duration time.Duration) *big.Int {
		seconds := int64(duration / time.Second)
		if duration%time.Second != 0 {
			seconds++
		}
		if seconds == 0 {
			return new(big.Int)
		}
		n := new(big.Int).Mul(big.NewInt(seconds), big.NewInt(int64(mb)))
		n.Mul(n, big.NewInt(rate))
		d := big.NewInt(quantity)
		n.Add(n, new(big.Int).Sub(d, big.NewInt(1))).Quo(n, d)
		return n.Add(n, big.NewInt(1))
	}
	firstEnd := from.Truncate(time.Minute).Add(time.Minute)
	if until.Before(firstEnd) {
		firstEnd = until
	}
	total := chunk(firstEnd.Sub(from))
	rest := until.Sub(firstEnd)
	minutes := int64(rest / time.Minute)
	total.Add(total, new(big.Int).Mul(big.NewInt(minutes), chunk(time.Minute)))
	total.Add(total, chunk(rest%time.Minute))
	if !total.IsInt64() {
		return 0, ErrOverflow
	}
	return total.Int64(), nil
}

func ComputeMonth(at time.Time) time.Time {
	return time.Date(at.UTC().Year(), at.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
}
