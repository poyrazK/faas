package verifycontract

import (
	"context"
	"errors"
	"time"
)

// Executor performs checks against one candidate. imaged implements it with
// the candidate-pinned hosting verifier, the MCP client and the SQL handoff
// to apid; tests implement it with fakes.
type Executor interface {
	HTTP(ctx context.Context, check HTTPCheck) (HTTPResponse, error)
	// MCP runs one MCP check. allowMutatingTools is false in production: a
	// tool call whose tool does not declare readOnlyHint must then return a
	// *CheckError with CodeMCPToolNotReadOnly without calling the tool.
	MCP(ctx context.Context, check MCPCheck, allowMutatingTools bool) error
	// SQL runs one read-only SQL check; name is the check's name.
	SQL(ctx context.Context, name string, check SQLCheck) (SQLResult, error)
}

// SQLResult is what a read-only query returned.
type SQLResult struct {
	RowCount int
	HasFirst bool
	First    *string
}

// CheckError is an executor outcome with a stable code. Assertion is true
// when the check ran and the candidate's answer was wrong (failed), false
// when no verdict was possible (error).
type CheckError struct {
	Code      string
	Message   string
	Assertion bool
}

func (e *CheckError) Error() string { return e.Message }

// RunOptions describe where the candidate runs.
type RunOptions struct {
	// Production is true for a production-scope deployment of a non-preview
	// app. Mutating checks are then skipped and reported.
	Production     bool
	ContractSHA256 string
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Run executes every check in order within the contract's time budget and
// returns the finished report. Checks run one at a time: candidates often
// allow a single concurrent request while the predecessor still serves.
func Run(ctx context.Context, contract Contract, exec Executor, opts RunOptions) Report {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	report := Report{ContractSHA256: opts.ContractSHA256, Environment: EnvironmentNonProduction, Checks: make([]CheckResult, 0, len(contract.Checks))}
	if opts.Production {
		report.Environment = EnvironmentProduction
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(contract.Timeout())*time.Second)
	defer cancel()
	for _, check := range contract.Checks {
		result := CheckResult{Name: check.Name, Kind: check.Kind(), Required: !check.Advisory}
		checkStarted := now()
		switch {
		case runCtx.Err() != nil:
			result.Status, result.Code = StatusError, CodeTimeout
			result.Failures = []string{"verification budget ran out before this check ran"}
		case opts.Production && check.Mutating():
			result.Status, result.Code = StatusSkipped, CodeMutationInProduction
			result.Failures = []string{"state-changing checks run only on previews and environment branches"}
		default:
			runCheck(runCtx, check, exec, opts.Production, &result)
		}
		result.DurationMS = now().Sub(checkStarted).Milliseconds()
		report.Checks = append(report.Checks, result)
	}
	report.DurationMS = now().Sub(started).Milliseconds()
	report.Finish()
	return report
}

func runCheck(ctx context.Context, check Check, exec Executor, production bool, result *CheckResult) {
	var failures []string
	var err error
	switch {
	case check.HTTP != nil:
		var resp HTTPResponse
		resp, err = exec.HTTP(ctx, *check.HTTP)
		if err == nil {
			result.HTTPStatus = resp.StatusCode
			failures = EvaluateHTTP(check.HTTP.Expect, resp)
		}
	case check.MCP != nil:
		err = exec.MCP(ctx, *check.MCP, !production)
	case check.SQL != nil:
		var res SQLResult
		res, err = exec.SQL(ctx, check.Name, *check.SQL)
		if err == nil {
			failures = EvaluateSQL(check.SQL.Expect, res.RowCount, res.HasFirst, res.First)
		}
	}
	switch {
	case err != nil:
		applyError(err, ctx, result)
	case len(failures) > 0:
		result.Status, result.Code, result.Failures = StatusFailed, CodeAssertionFailed, failures
	default:
		result.Status = StatusPassed
	}
}

func applyError(err error, ctx context.Context, result *CheckResult) {
	var checkErr *CheckError
	switch {
	case errors.As(err, &checkErr):
		result.Code = checkErr.Code
		result.Failures = []string{checkErr.Message}
		result.Status = StatusError
		if checkErr.Assertion {
			result.Status = StatusFailed
		}
		// A tool that does not declare itself read-only is a mutating
		// check: in production it is skipped, like a POST.
		if checkErr.Code == CodeMCPToolNotReadOnly {
			result.Status = StatusSkipped
		}
	case ctx.Err() != nil:
		result.Status, result.Code = StatusError, CodeTimeout
		result.Failures = []string{"verification budget ran out during this check"}
	default:
		result.Status, result.Code = StatusError, CodeRequestFailed
		result.Failures = []string{"the check could not reach the candidate"}
	}
}
