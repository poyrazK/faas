package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRunControlConflict = errors.New("state: Run control revision conflict")
var ErrRunAttributionDenied = errors.New("state: Run attribution does not match authenticated binding")

type RunBudgetError struct {
	Kind, Ref, Metric string
	Maximum, Observed int64
}

func (e *RunBudgetError) Error() string {
	return "Run budget admission refused: " + e.Kind + "/" + e.Ref + "/" + e.Metric
}

type RunHistoryQuery struct {
	AccountID, PrincipalID string
	Wide                   bool
	Filter                 api.RunHistoryFilter
	Before                 time.Time
	BeforeID               string
	Limit                  int
}
type RunControlStore interface {
	PutRunBudget(context.Context, string, string, string, api.SetRunBudgetRequest) (api.RunBudgetPolicy, error)
	GetRunBudget(context.Context, string, string, string) (api.RunBudgetPolicy, error)
	PutRunBinding(context.Context, string, string, string, api.SetRunAttributionBindingRequest) (api.RunAttributionBinding, error)
	GetRunBinding(context.Context, string, string, string) (api.RunAttributionBinding, error)
	DeleteRunBinding(context.Context, string, string, string, int64) error
	ListRunHistory(context.Context, RunHistoryQuery) ([]api.RunHistoryEntry, error)
}
type runControlReservation struct {
	AccountID    string
	Attribution  api.RunAttribution
	TaskName     string
	ReservedMBMS int64
	AdmittedAt   time.Time
}

func runControlKey(account, kind, ref string) string { return account + "\x00" + kind + "\x00" + ref }
func validRunPolicy(kind, ref string, req api.SetRunBudgetRequest) bool {
	if (kind != "customer" && kind != "task") || !api.ValidRunControlRef(ref) || req.ExpectedRevision < 0 {
		return false
	}
	for _, v := range []struct {
		p   *int64
		max int64
	}{{req.Limits.MaxConcurrent, api.RunControlMaxConcurrent}, {req.Limits.MaxInvocationsPerDay, api.RunControlMaxDailyInvocations}, {req.Limits.MaxComputeMBMSPerDay, api.RunControlMaxDailyComputeMBMS}} {
		if v.p != nil && (*v.p < 0 || *v.p > v.max) {
			return false
		}
	}
	return true
}
func validRunSubject(kind, id string) bool {
	_, err := uuid.Parse(id)
	return (kind == "api_key" || kind == "agent_endpoint") && err == nil
}
func cloneRunPolicy(p api.RunBudgetPolicy) api.RunBudgetPolicy {
	p.Limits.MaxConcurrent = copyRunBudgetLimit(p.Limits.MaxConcurrent)
	p.Limits.MaxInvocationsPerDay = copyRunBudgetLimit(p.Limits.MaxInvocationsPerDay)
	p.Limits.MaxComputeMBMSPerDay = copyRunBudgetLimit(p.Limits.MaxComputeMBMSPerDay)
	p.Alerts = append([]api.RunBudgetAlert{}, p.Alerts...)
	return p
}
func copyRunBudgetLimit(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func runControlDay(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}
func runReservationMBMS(p CreateExecutionParams) int64 {
	return int64(p.Request.Limits.MemoryMB+api.PerVMOverheadMB) * int64(p.Request.Limits.TimeoutMS)
}

func runAttribution(p CreateExecutionParams, bindings []api.RunAttributionBinding) (api.RunAttribution, error) {
	var a api.RunAttribution
	for _, b := range bindings {
		if a.CustomerRef != "" && a.CustomerRef != b.CustomerRef {
			return a, ErrRunAttributionDenied
		}
		a = b.RunAttribution
		if a.AgentRef == "" && b.SubjectKind == "agent_endpoint" {
			a.AgentRef = b.SubjectKind + ":" + b.SubjectID
		}
	}
	if p.CustomerRef != "" {
		if !api.ValidRunControlRef(p.CustomerRef) || a.CustomerRef != "" && a.CustomerRef != p.CustomerRef || a.CustomerRef == "" && !p.ControlAccountWide {
			return a, ErrRunAttributionDenied
		}
		a.CustomerRef = p.CustomerRef
	}
	if a.AgentRef == "" {
		if endpoint := runCallbackEndpoint(p.Task); endpoint != "" {
			a.AgentRef = "agent_endpoint:" + endpoint
		}
	}
	return a, nil
}
func checkRunBudget(p api.RunBudgetPolicy, u api.RunBudgetUsage, reserved int64) error {
	if !p.Enabled {
		return &RunBudgetError{Kind: p.Kind, Ref: p.Ref, Metric: "enabled", Maximum: 0, Observed: 1}
	}
	for _, v := range []struct {
		metric   string
		max      *int64
		observed int64
	}{
		{"concurrent", p.Limits.MaxConcurrent, u.Active + 1},
		{"invocations", p.Limits.MaxInvocationsPerDay, u.Invocations + 1},
		{"compute_mb_ms", p.Limits.MaxComputeMBMSPerDay, u.ComputeUsedMBMS + u.ComputeReservedMBMS + reserved},
	} {
		if v.max != nil && v.observed > *v.max {
			return &RunBudgetError{p.Kind, p.Ref, v.metric, *v.max, v.observed}
		}
	}
	return nil
}

// Settlement is the existing execution-ID-keyed terminal ledger joined to its
// immutable reservation. No refund worker or second terminal writer is needed.
func settledRunCompute(reserved, timeout, wall int64) int64 {
	if reserved == 0 {
		return 0
	}
	if timeout <= 0 {
		return reserved
	}
	coefficient := reserved / timeout
	if coefficient <= 0 || wall > reserved/coefficient {
		return reserved
	}
	return min(reserved, coefficient*max(wall, 0))
}

func runBudgetAlerts(account string, p api.RunBudgetPolicy, u api.RunBudgetUsage, at time.Time) []api.RunBudgetAlert {
	out := []api.RunBudgetAlert{}
	for _, v := range []struct {
		metric string
		limit  *int64
		used   int64
	}{
		{"concurrent", p.Limits.MaxConcurrent, u.Active},
		{"invocations", p.Limits.MaxInvocationsPerDay, u.Invocations},
		{"compute_mb_ms", p.Limits.MaxComputeMBMSPerDay, u.ComputeUsedMBMS + u.ComputeReservedMBMS},
	} {
		if v.limit == nil || *v.limit == 0 {
			continue
		}
		for _, percent := range []int{api.RunBudgetWarningPercent, 100} {
			if v.used*100 < *v.limit*int64(percent) {
				continue
			}
			identity := fmt.Sprintf("gregale run budget v1\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d", account, p.Kind, p.Ref, u.Day, v.metric, percent)
			out = append(out, api.RunBudgetAlert{EventID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String(), Kind: p.Kind, Ref: p.Ref, Day: u.Day, Metric: v.metric, ThresholdPercent: percent, Limit: *v.limit, Observed: v.used, CreatedAt: at.UTC().Format(time.RFC3339Nano)})
		}
	}
	return out
}
func runHistoryEntry(r Execution, at time.Time) api.RunHistoryEntry {
	v := api.RunHistoryEntry{ID: r.ID, Task: r.Task, Attribution: r.Attribution, Status: r.Status, Runtime: r.Runtime, CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano), ReceiptURL: "/v1/executions/" + r.ID}
	end := at
	if r.StartedAt != nil {
		s := r.StartedAt.UTC().Format(time.RFC3339Nano)
		v.StartedAt = &s
	}
	if r.FinishedAt != nil {
		s := r.FinishedAt.UTC().Format(time.RFC3339Nano)
		v.FinishedAt = &s
		end = *r.FinishedAt
	}
	v.ElapsedMS = max(0, end.Sub(r.CreatedAt).Milliseconds())
	if r.Status.Terminal() {
		u := r.Usage
		v.Usage = &u
	}
	if r.ResultValidation != nil {
		v.ResultValidationStatus = r.ResultValidation.Status
	}
	if r.FailureCode != nil {
		v.FailureCode = *r.FailureCode
	}
	return v
}

func runBudgetAlertID(account string, a api.RunBudgetAlert) string {
	identity := fmt.Sprintf("gregale run budget v1\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d", account, a.Kind, a.Ref, a.Day, a.Metric, a.ThresholdPercent)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String()
}
