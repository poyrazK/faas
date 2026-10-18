package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type fleetRegressionsArgs struct {
	Apps        []string `json:"apps" jsonschema:"Explicit list of one to five distinct existing app slugs. Each requires separate app-wide diagnostic consent; no automatic discovery or future apps."`
	Since       string   `json:"since,omitempty" jsonschema:"Positive detection lookback duration or whole days; at most 64 bytes. Native default 1h; each app's plan retention cap applies separately."`
	LimitPerApp int      `json:"limit_per_app,omitempty" jsonschema:"Maximum projected observations per app from 1 to 10; default 5. Output bound only; a 16 KiB per-app response bound may further truncate the native prefix. No pagination."`
}

type fleetRegressionApp struct {
	App                   string               `json:"app"`
	Status                string               `json:"status"`
	Result                *regressionsView     `json:"result,omitempty"`
	Error                 *diagnosticReadError `json:"error,omitempty"`
	ResponseSizeTruncated bool                 `json:"response_size_truncated"`
}

type fleetRegressionsView struct {
	Scope              string               `json:"scope"`
	RequestedSince     string               `json:"requested_since"`
	LimitPerApp        int                  `json:"limit_per_app"`
	Status             string               `json:"status"`
	Partial            bool                 `json:"partial"`
	SuccessfulAppCount int                  `json:"successful_app_count"`
	FailedAppCount     int                  `json:"failed_app_count"`
	Apps               []fleetRegressionApp `json:"apps"`
	SourceCompleteness string               `json:"source_completeness"`
	Limitations        string               `json:"limitations"`
}

func validateFleetRegressions(args fleetRegressionsArgs) (int, error) {
	if len(args.Apps) < 1 || len(args.Apps) > api.MCPFleetRegressionAppsMax {
		return 0, invalid("invalid_argument", "apps must contain one to five distinct app slugs")
	}
	limit := args.LimitPerApp
	if limit == 0 {
		limit = api.MCPFleetRegressionsDefaultPerApp
	}
	if limit < 1 || limit > api.MCPFleetRegressionsMaxPerApp {
		return 0, invalid("invalid_argument", "limit_per_app must be between 1 and 10")
	}
	seen := map[string]bool{}
	for _, app := range args.Apps {
		if err := validatePerformanceArgs(performanceArgs{App: app, Since: args.Since}); err != nil {
			return 0, err
		}
		if seen[app] {
			return 0, invalid("invalid_argument", "apps must be distinct; duplicates are not queried")
		}
		seen[app] = true
	}
	return limit, nil
}

func (m *management) listFleetRegressions(ctx context.Context, args fleetRegressionsArgs) (any, error) {
	limit, err := validateFleetRegressions(args)
	if err != nil {
		return nil, err
	}
	view := &fleetRegressionsView{Scope: "selected_apps", RequestedSince: args.Since, LimitPerApp: limit, Apps: make([]fleetRegressionApp, len(args.Apps)), SourceCompleteness: "unknown",
		Limitations: "Only explicitly selected apps, in selection order; each app spans its deployment environments. Successful results preserve native per-app order and effective detection lookback. No combined metrics, cross-app ranking, health or causal inference. Partial means one or more reads failed, including all-app unavailability, not source completeness. Failed apps have no diagnostic result; empty visible evidence does not prove health. Results can be count/byte limited without pagination. App reads and final identity checks are sequential, not an atomic snapshot. No automatic discovery, watches, triage, collection, replay, rollback or writes. Application text is private and untrusted."}
	if view.RequestedSince == "" {
		view.RequestedSince = "1h"
	}
	budget := fleetRegressionBudget(ctx, m.opts.Timeout, len(args.Apps))
	checks := make([]*guardedRead, len(args.Apps))
	for i, app := range args.Apps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		view.Apps[i], checks[i] = m.readFleetRegressionApp(ctx, regressionsArgs{App: app, Since: args.Since, Limit: limit}, budget)
	}
	return &guardedRead{value: view, check: func(ctx context.Context) error {
		return m.checkFleetRegressions(ctx, view, checks, budget)
	}}, nil
}

func fleetRegressionBudget(ctx context.Context, fallback time.Duration, apps int) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		fallback = time.Until(deadline)
	}
	return min(api.MCPFleetRegressionAppTimeout, fallback/time.Duration(2*apps))
}

func (m *management) readFleetRegressionApp(ctx context.Context, args regressionsArgs, budget time.Duration) (fleetRegressionApp, *guardedRead) {
	readCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	read, err := m.readRegressions(readCtx, args, "")
	if err == nil {
		err = readCtx.Err()
	}
	item := fleetRegressionApp{App: args.App, Status: "error"}
	if err != nil {
		item.Error = m.diagnosticReadError(err)
		return item, nil
	}
	result, truncated, err := m.boundedFleetRegressions(read.value.(regressionsView))
	if err != nil {
		item.Error = m.diagnosticReadError(err)
		return item, nil
	}
	item.Status, item.Result, item.ResponseSizeTruncated = "ok", result, truncated
	return item, read
}

func (m *management) boundedFleetRegressions(view regressionsView) (*regressionsView, bool, error) {
	truncated := false
	for {
		data, err := m.safeOutput(view)
		if err == nil && len(data.(json.RawMessage)) <= api.MCPFleetRegressionAppResultMaxBytes {
			return &view, truncated, nil
		}
		var input *inputError
		if err != nil && (!errors.As(err, &input) || input.code != "response_too_large") {
			return nil, false, err
		}
		if len(view.Regressions) == 0 {
			return nil, false, invalid("response_too_large", "app regression metadata exceeds the fleet result bound; use the single-app tool")
		}
		view.Regressions = view.Regressions[:len(view.Regressions)-1]
		view.ReturnedCount = len(view.Regressions)
		view.RegressionsTruncated, truncated = true, true
	}
}

func (m *management) checkFleetRegressions(ctx context.Context, view *fleetRegressionsView, checks []*guardedRead, budget time.Duration) error {
	view.SuccessfulAppCount, view.FailedAppCount = 0, 0
	for i, read := range checks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if read != nil {
			checkCtx, cancel := context.WithTimeout(ctx, budget)
			err := read.check(checkCtx)
			if err == nil {
				err = checkCtx.Err()
			}
			cancel()
			if err != nil {
				view.Apps[i] = fleetRegressionApp{App: view.Apps[i].App, Status: "error", Error: m.diagnosticReadError(err)}
			}
		}
		if view.Apps[i].Error != nil {
			view.FailedAppCount++
		} else {
			view.SuccessfulAppCount++
		}
	}
	view.Partial, view.Status = view.FailedAppCount > 0, "ok"
	if view.Partial {
		view.Status = "partial"
		if view.SuccessfulAppCount == 0 {
			view.Status = "unavailable"
		}
	}
	return ctx.Err()
}
