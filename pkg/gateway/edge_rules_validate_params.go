package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// validateEdgeRuleParameters checks a kind=validate rule's path, query, and
// header schemas (ADR-091 amendment: request parameters) before the body is
// read. It returns true when the request was answered: a block-mode
// mismatch (422) or a broken schema. Observe and warn mismatches are
// recorded and the remaining checks still run.
func (h *Handler) validateEdgeRuleParameters(w http.ResponseWriter, r *http.Request, rec *statusRecorder, rule *EdgeRuleValidateResolved) bool {
	p := rule.Parameters
	for _, loc := range []struct {
		name   string
		schema *EdgeRuleParamSchemaResolved
	}{{"path", &p.Path}, {"query", &p.Query}, {"headers", &p.Headers}} {
		if !loc.schema.Set {
			continue
		}
		values, ok := edgeRuleParamValues(r, loc.name, p.PathTemplate, loc.schema.Kinds)
		var res *EdgeValidateResult
		if !ok {
			// The rule glob matched but the path does not fit the template.
			res = &EdgeValidateResult{FirstError: &EdgeValidateFieldError{Field: "path", Expected: "path_template", Got: r.URL.EscapedPath()}}
		} else {
			instance, err := api.EdgeRuleParamInstance(loc.schema.Kinds, values)
			if err == nil {
				digest := loc.schema.Digest
				res, err = h.validator.Validate(r.Context(), &EdgeValidateIn{
					Body: instance, ContentType: "application/json", Digest: &digest,
				}, rule)
			}
			if err != nil {
				h.writeEdgeRuleValidateError(w, r, rule, err)
				return true
			}
		}
		if res.OK {
			continue
		}
		if res.FirstError != nil {
			fe := *res.FirstError
			fe.Field = loc.name + fe.Field
			res = &EdgeValidateResult{SchemaDigest: res.SchemaDigest, FirstError: &fe}
		}
		if h.handleEdgeRuleValidateMismatch(w, r, rec, rule, res,
			fmt.Sprintf("%s parameters do not match schema for rule %s", loc.name, rule.ID)) {
			return true
		}
	}
	return false
}

// edgeRuleParamValues collects one location's raw values. Query returns
// every parameter; headers return only declared names, splitting comma lists
// for array properties.
func edgeRuleParamValues(r *http.Request, location, pathTemplate string, kinds map[string]api.EdgeRuleParamKind) (map[string][]string, bool) {
	switch location {
	case "path":
		values, ok := api.PathTemplateValues(pathTemplate, r.URL.EscapedPath())
		if !ok {
			return nil, false
		}
		out := make(map[string][]string, len(values))
		for name, v := range values {
			out[name] = []string{v}
		}
		return out, true
	case "query":
		return r.URL.Query(), true
	default:
		out := make(map[string][]string, len(kinds))
		for name, kind := range kinds {
			raw := r.Header.Values(name)
			if len(raw) == 0 {
				continue
			}
			if kind.Type != "array" {
				out[name] = raw
				continue
			}
			var items []string
			for _, v := range raw {
				for _, item := range strings.Split(v, ",") {
					items = append(items, strings.TrimSpace(item))
				}
			}
			out[name] = items
		}
		return out, true
	}
}

// writeEdgeRuleValidateError answers a request whose validate rule could not
// be evaluated (a broken or missing compiled schema).
func (h *Handler) writeEdgeRuleValidateError(w http.ResponseWriter, r *http.Request, rule *EdgeRuleValidateResolved, err error) {
	switch {
	case errors.Is(err, ErrValidateSchemaExternalRef):
		// Compile-time defense fired at runtime — shouldn't happen if
		// apid-Validate was correct. 502 signals "the gateway dependency
		// is broken"; ops will see the alarm + slog.
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway,
			api.CodeBadGateway, "Edge rule compile error",
			"validate rule contains an external $ref/$id; refusing to validate"))
	case errors.Is(err, ErrValidateSchemaInvalid),
		errors.Is(err, ErrValidateSchemaEmpty),
		errors.Is(err, ErrValidateSchemaTooLarge):
		// Broken stored schema — deploy bug. 500.
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "Edge rule schema error",
			"validate rule schema is broken"))
	default:
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal, "Edge rule validator error", err.Error()))
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_failed", nil, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"reason":    "validator_error",
			"err":       err.Error(),
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("validate", "failed")
		// PR-C: validator error (502 / 500) is a non-2xx wire write —
		// emit apply error so the §12 chip surfaces the broken rule.
		h.metrics.ObserveEdgeRuleApply("validate", "error")
	}
}

// handleEdgeRuleValidateMismatch applies the rule's validate_mode to a schema
// mismatch and reports whether the request was rejected (block mode, 422).
//
// validate_mode (issue #975 #3 / Mega-Foundation #979-a): default empty ==
// 'block' to match the schema-side default at 00293. `observe` and `warn`
// never reject — they count the failure in the validate_failures metric and
// let the proxy leg run. `warn` additionally stamps X-Validation-Warning:
// <rule_id> via the statusRecorder so the customer's API consumer can see the
// warning without the gateway changing the response status.
func (h *Handler) handleEdgeRuleValidateMismatch(w http.ResponseWriter, r *http.Request, rec *statusRecorder, rule *EdgeRuleValidateResolved, res *EdgeValidateResult, detail string) bool {
	// res.FirstError may be nil if the schema failed but the library
	// returned no FieldError — treat as a generic 422 with an empty errors
	// slice.
	var errs []api.FieldError
	if res.FirstError != nil {
		errs = []api.FieldError{{
			Field:    res.FirstError.Field,
			Expected: res.FirstError.Expected,
			Got:      res.FirstError.Got,
		}}
	}
	mode := rule.ValidateMode
	if mode == "" {
		mode = api.ValidateModeBlock
	}
	reason := reasonOther
	if res.FirstError != nil {
		reason = res.FirstError.Reason()
	}
	if h.metrics != nil {
		// ADR-128 §5: appID + ruleID localize failures; the rule_id label
		// is admitted through ruleLabelSet (cap 256 per app; overflow →
		// "__other__") so the series set stays bounded.
		h.metrics.ObserveEdgeRuleValidateFailure(rule.AppID, rule.ID, mode, reason)
	}
	if h.edgeRuleAudit != nil {
		auditData := map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"reason":    "schema_mismatch",
			"mode":      mode,
		}
		if res.FirstError != nil {
			auditData["field"] = res.FirstError.Field
			auditData["expected"] = res.FirstError.Expected
		}
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.validate_failed", nil, auditData)
	}
	switch mode {
	case api.ValidateModeObserve, api.ValidateModeWarn:
		if mode == api.ValidateModeWarn {
			// The header value is the rule ID, not the failing field,
			// keeping any PII in the field path out of the response.
			rec.installHeaderOps([]EdgeRuleHeaderOp{
				{Action: "set", Name: "X-Validation-Warning", Value: rule.ID},
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("validate", "match")
			h.metrics.ObserveEdgeRuleApply("validate", "success")
		}
		return false
	default:
		api.WriteProblemWithErrors(w, api.NewProblem(http.StatusUnprocessableEntity,
			api.CodeRequestValidationFailed, "Invalid request", detail), errs)
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("validate", "blocked")
			// PR-C: 422 schema mismatch is a non-2xx wire write — emit
			// apply error so the §12 chip surfaces the malformed payload.
			h.metrics.ObserveEdgeRuleApply("validate", "error")
		}
		return true
	}
}
