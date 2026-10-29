// adr:1367 — progress is best-effort stderr output, never execution authority.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingrun"
)

type bindingProgressKey struct{}

func withBindingProgress(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, bindingProgressKey{}, mode)
}
func validBindingProgress(mode string) bool {
	return slices.Contains([]string{"auto", "plain", "ndjson", "off"}, mode)
}
func bindingProgressMode(ctx context.Context) string {
	mode, _ := ctx.Value(bindingProgressKey{}).(string)
	if mode == "" || mode == "auto" {
		if jsonOutput {
			return "off"
		}
		return "plain"
	}
	return mode
}

type bindingRecoverySummary struct {
	RunID              string                                `json:"run_id"`
	LastStatus         string                                `json:"last_status,omitempty"`
	LastOperation      string                                `json:"last_operation,omitempty"`
	LastQualification  *api.BindingVerificationQualification `json:"last_qualification,omitempty"`
	StatusCommand      string                                `json:"status_command"`
	DiagnosticsCommand string                                `json:"diagnostics_command"`
}
type bindingProgressView struct {
	mode, id, app, project, environment string
	seen                                map[string]string
	lastStatus, lastOperation           string
	lastQualification                   *api.BindingVerificationQualification
}

func newBindingProgress(ctx context.Context, id, app, project, environment string) *bindingProgressView {
	return &bindingProgressView{mode: bindingProgressMode(ctx), id: id, app: app, project: project, environment: environment, seen: map[string]string{}}
}
func copyBindingQualification(q *api.BindingVerificationQualification) *api.BindingVerificationQualification {
	if q == nil {
		return nil
	}
	out := *q
	out.Conditions = slices.Clone(q.Conditions)
	return &out
}
func (v *bindingProgressView) observe(p bindingrun.Progress) error {
	// Retain the last waiting observation even if a later GET omits ephemeral conditions.
	if p.Step == -1 || p.Status == "reconciling" || p.Status == "retrying" {
		v.lastStatus = p.Status
	}
	if p.Operation != "" {
		v.lastOperation = p.Operation
	}
	if p.Qualification != nil {
		v.lastQualification = copyBindingQualification(p.Qualification)
	}
	if v.mode == "off" || v.mode == "plain" && p.Step >= 0 && p.ResourceID == "" && p.Status == "planned" {
		return nil
	}
	key := fmt.Sprintf("%s/%d", p.App, p.Step)
	fingerprint := p
	fingerprint.Qualification = copyBindingQualification(p.Qualification)
	if fingerprint.Qualification != nil {
		fingerprint.Qualification.CheckedAt = time.Time{}
	}
	data, _ := json.Marshal(fingerprint)
	if v.seen[key] == string(data) {
		return nil
	}
	v.seen[key] = string(data)
	if v.mode == "ndjson" {
		_ = json.NewEncoder(osStderr).Encode(struct {
			Type string `json:"type"`
			bindingrun.Progress
			Project     string `json:"project,omitempty"`
			Environment string `json:"environment,omitempty"`
		}{"binding_verification_progress", p, v.project, v.environment})
		return nil
	}
	label := fmt.Sprintf("Verification run %s", p.RunID)
	if p.App != "" {
		label += fmt.Sprintf(" app=%q", p.App)
	}
	if p.Step >= 0 {
		label += fmt.Sprintf(" step=%d", p.Step)
	}
	if p.ResourceID != "" {
		label += fmt.Sprintf(" %s=%s", p.ResourceKind, p.ResourceID)
	}
	_, _ = fmt.Fprintf(osStderr, "%s: %s", label, p.Status)
	if p.Operation != "" {
		_, _ = fmt.Fprintf(osStderr, " operation=%s", p.Operation)
	}
	if p.RetryAttempt > 0 {
		_, _ = fmt.Fprintf(osStderr, " attempt=%d delay=%dms", p.RetryAttempt, p.RetryDelayMilliseconds)
	}
	_, _ = fmt.Fprintln(osStderr)
	renderBindingWaiting(osStderr, p.Qualification)
	return nil
}
func (v *bindingProgressView) recovery() *bindingRecoverySummary {
	prefix := fmt.Sprintf("gregale app binding verification-runs %%s %s %s", v.app, v.id)
	if v.project != "" {
		prefix = fmt.Sprintf("gregale environment release-sets verification-runs %%s %s %s %s", v.project, v.environment, v.id)
	}
	return &bindingRecoverySummary{RunID: v.id, LastStatus: v.lastStatus, LastOperation: v.lastOperation, LastQualification: copyBindingQualification(v.lastQualification), StatusCommand: fmt.Sprintf(prefix, "status"), DiagnosticsCommand: fmt.Sprintf(prefix, "diagnostics")}
}
func announceBindingRun(ctx context.Context, id, app, project, environment string) {
	view := newBindingProgress(ctx, id, app, project, environment)
	if view.mode == "ndjson" {
		_ = view.observe(bindingrun.Progress{RunID: id, App: app, Step: -1, Status: "creating"})
		return
	}
	// Preserve the pre-create recovery ID even when live progress is disabled.
	label := "Verification run"
	if project != "" {
		label = "Graph verification run"
	}
	_, _ = fmt.Fprintf(osStderr, "%s %s; inspect with %s\n", label, id, view.recovery().StatusCommand)
}

func renderBindingWaiting(out io.Writer, q *api.BindingVerificationQualification) {
	if q == nil {
		return
	}
	for _, c := range q.Conditions[:min(3, len(q.Conditions))] {
		_, _ = fmt.Fprintf(out, "  %s app=%q scope=%q binding=%q name=%q\n", c.Code, c.App, c.Scope, c.Binding, c.Name)
	}
	if len(q.Conditions) > 3 {
		_, _ = fmt.Fprintf(out, "  %d more waiting conditions are in the JSON report.\n", len(q.Conditions)-3)
	}
}
func renderBindingRecovery(r *bindingRecoverySummary) {
	if r == nil {
		return
	}
	_, _ = fmt.Fprintf(osStderr, "Verification stopped; original receipt %s, last observed status=%s\n", r.RunID, r.LastStatus)
	if r.LastQualification != nil {
		_, _ = fmt.Fprintf(osStderr, "Last waiting observation (%s):\n", r.LastQualification.CheckedAt.Format(time.RFC3339))
		renderBindingWaiting(osStderr, r.LastQualification)
	}
	_, _ = fmt.Fprintf(osStderr, "Inspect: %s\nDiagnostics: %s\n", r.StatusCommand, r.DiagnosticsCommand)
}

func (v *bindingProgressView) notice(code string, recovery *bindingRecoverySummary) {
	_ = json.NewEncoder(osStderr).Encode(struct {
		Type     string                  `json:"type"`
		RunID    string                  `json:"run_id"`
		Code     string                  `json:"code"`
		Recovery *bindingRecoverySummary `json:"recovery,omitempty"`
	}{"binding_verification_notice", v.id, code, recovery})
}
func (v *bindingProgressView) renderReport(r bindingPolicyVerificationReport) {
	if v.mode != "ndjson" {
		renderBindingPolicyVerification(r)
		return
	}
	summary, code := r.Recovery, r.ErrorCode
	r.Recovery = nil
	r.Diagnostics = nil
	r.Error = ""
	renderBindingPolicyVerification(r)
	if summary != nil {
		v.notice(code, summary)
	}
}
