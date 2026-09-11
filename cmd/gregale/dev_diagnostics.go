package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// These client-only codes cover failures that can be observed in a runtime
// log but do not have a server-side RFC 7807 code yet. They are deliberately
// namespaced so JSON consumers can branch on them without confusing them with
// persisted deployment error codes.
const (
	devDiagSyncFailed      = "developer_sync_failed"
	devDiagStartupCrash    = "developer_startup_crash"
	devDiagImportFailure   = "developer_import_failure"
	devDiagBindFailure     = "developer_bind_failure"
	devDiagUpstreamFailure = "developer_upstream_failure"
)

type devDiagnostic struct {
	Event        string           `json:"event"`
	Code         string           `json:"code"`
	Title        string           `json:"title"`
	Detail       string           `json:"detail,omitempty"`
	Phase        string           `json:"phase,omitempty"`
	Hint         string           `json:"hint,omitempty"`
	Why          string           `json:"why,omitempty"`
	Fix          string           `json:"fix,omitempty"`
	DeploymentID string           `json:"deployment_id,omitempty"`
	LogsCommand  string           `json:"logs_command,omitempty"`
	SourceDir    string           `json:"source_dir,omitempty"`
	Logs         []api.LogExcerpt `json:"relevant_logs,omitempty"`
	Catalog      bool             `json:"-"`
}

func devDiagnosticFromDeployment(dep api.DeploymentResponse, phase, reason string) devDiagnostic {
	code := dep.ErrorCode
	if code == "" {
		code = inferDevDiagnosticCode(dep.Error, phase, reason)
	}
	p := &api.Problem{Code: code, Detail: dep.Error}
	decorateDevDiagnosticProblem(p, code)
	if p.Title == "" {
		p.Title = "Developer sync failed"
	}
	if p.Hint == "" {
		p.Hint = "fix the reported failure, then save again to retry the latest source"
	}
	if p.Fix == "" {
		p.Fix = "check the runtime and build logs, then save again"
	}
	d := devDiagnostic{
		Event:        "developer_diagnostic",
		Code:         code,
		Title:        p.Title,
		Detail:       p.Detail,
		Phase:        devDiagnosticPhase(phase),
		Hint:         p.Hint,
		Why:          p.Why,
		Fix:          p.Fix,
		DeploymentID: dep.ID,
		Logs:         dep.ErrorRelevantLogs,
		Catalog:      dep.ErrorCode != "",
	}
	if dep.ID != "" {
		d.LogsCommand = fmt.Sprintf("gregale logs --deployment %s", dep.ID)
	}
	return d
}

func devDiagnosticFromError(err error, phase string) devDiagnostic {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		p := apiErr.Problem
		code := p.Code
		if code == "" {
			code = inferDevDiagnosticCode(p.Detail, phase, "")
		}
		decorateDevDiagnosticProblem(&p, code)
		if p.Title == "" {
			p.Title = "Developer sync failed"
		}
		if p.Hint == "" {
			p.Hint = "fix the reported failure, then save again to retry the latest source"
		}
		d := devDiagnostic{
			Event:   "developer_diagnostic",
			Code:    code,
			Title:   p.Title,
			Detail:  p.Detail,
			Phase:   devDiagnosticPhase(phase),
			Hint:    p.Hint,
			Why:     p.Why,
			Fix:     p.Fix,
			Logs:    p.RelevantLogs,
			Catalog: p.Hint != "" || p.Why != "" || p.Fix != "",
		}
		return d
	}

	d := devDiagnosticFromText(errString(err), phase)
	d.Detail = errString(err)
	return d
}

func devDiagnosticFromText(text, phase string) devDiagnostic {
	code := inferDevDiagnosticCode(text, phase, "")
	p := &api.Problem{Code: code, Detail: text}
	decorateDevDiagnosticProblem(p, code)
	if p.Title == "" {
		p.Title = "Developer sync failed"
	}
	if p.Hint == "" {
		p.Hint = "fix the reported failure, then save again to retry the latest source"
	}
	if p.Fix == "" {
		p.Fix = "check the runtime logs, then save again"
	}
	return devDiagnostic{
		Event:   "developer_diagnostic",
		Code:    code,
		Title:   p.Title,
		Detail:  text,
		Phase:   devDiagnosticPhase(phase),
		Hint:    p.Hint,
		Why:     p.Why,
		Fix:     p.Fix,
		Catalog: false,
	}
}

func decorateDevDiagnosticProblem(p *api.Problem, code string) {
	if p == nil || code == "" {
		return
	}
	_ = whycopy.Decorate(p, code, nil)
	switch code {
	case devDiagImportFailure:
		p.Title = "Runtime import or module failure"
		p.Hint = "the application could not load a module during startup"
		p.Why = "the runtime log contains a missing-module error, so the process exited before it could serve requests"
		p.Fix = "• add the module to the declared dependency file\n• regenerate and commit the lockfile\n• save again to rebuild the developer environment"
	case devDiagBindFailure:
		p.Title = "Application failed to bind its port"
		p.Hint = "the process could not claim the configured listening port"
		p.Why = "the runtime reported that the listening address is already in use, so the server never became reachable"
		p.Fix = "• bind exactly once to process.env.PORT (or the framework equivalent)\n• stop any second development server started by your code\n• save again to retry"
	case devDiagUpstreamFailure:
		p.Title = "Upstream connection failed"
		p.Hint = "a dependency connection was refused from the developer environment"
		p.Why = "the runtime log shows an upstream connection failure while the developer environment was handling a request or starting up"
		p.Fix = "• verify the dependency URL, port, and credentials\n• use a local service override for development dependencies\n• save again after correcting the configuration"
	case devDiagStartupCrash:
		p.Title = "Application crashed during startup"
		p.Hint = "the process exited before it became ready"
		p.Why = "the runtime log contains a panic, traceback, or uncaught exception before the developer environment could serve traffic"
		p.Fix = "• reproduce the startup command locally\n• fix the first exception in the runtime log\n• save again to retry the latest source"
	case devDiagSyncFailed:
		p.Title = "Developer sync failed"
		p.Hint = "the latest source did not become live"
		p.Fix = "check the build and runtime logs, then save again"
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func inferDevDiagnosticCode(detail, phase, reason string) string {
	text := strings.ToLower(detail + " " + reason)
	switch {
	case strings.Contains(text, "out of memory"), strings.Contains(text, "oomkilled"), strings.Contains(text, "oom-kill"):
		if phase == "image_build" || phase == "build" {
			return api.CodeStageImageBuildOOM
		}
		return api.CodeAppRuntimeOOM
	case strings.Contains(text, "timed out"), strings.Contains(text, "timeout"):
		if phase == "readiness" || phase == "snapshot_prepare" {
			if phase == "readiness" {
				return api.CodeStageReadinessFailed
			}
			return api.CodeStageSnapshotPrepareTimeout
		}
		return api.CodeStageImageBuildTimeout
	case strings.Contains(text, "cannot find module"), strings.Contains(text, "module not found"), strings.Contains(text, "modulenotfounderror"), strings.Contains(text, "no module named"):
		return devDiagImportFailure
	case strings.Contains(text, "eaddrinuse"), strings.Contains(text, "address already in use"):
		return devDiagBindFailure
	case strings.Contains(text, "eaddrnotavail"), strings.Contains(text, "loopback"), strings.Contains(text, "127.0.0.1"):
		return api.CodeAppLoopbackBound
	case strings.Contains(text, "econnrefused"), strings.Contains(text, "connection refused"), strings.Contains(text, "upstream"):
		return devDiagUpstreamFailure
	case strings.Contains(text, "exec format"), strings.Contains(text, "enoexec"), strings.Contains(text, "architecture"):
		return api.CodeAppArchMismatch
	case strings.Contains(text, "healthz") && (strings.Contains(text, "401") || strings.Contains(text, "403")):
		return api.CodeAppHealthzUnauthorized
	case strings.Contains(text, "panic"), strings.Contains(text, "traceback"), strings.Contains(text, "uncaught exception"), strings.Contains(text, "fatal error"):
		return devDiagStartupCrash
	case phase == "readiness":
		return api.CodeStageReadinessFailed
	default:
		return devDiagSyncFailed
	}
}

func devDiagnosticPhase(phase string) string {
	switch phase {
	case "source_sync":
		return "sync"
	case "dependency_restore":
		return "dependencies"
	case "image_build":
		return "build"
	case "snapshot_prepare":
		return "boot"
	default:
		return phase
	}
}

func classifyDevRuntimeLog(line string) (devDiagnostic, bool) {
	d := devDiagnosticFromText(line, "runtime")
	switch d.Code {
	case devDiagImportFailure, devDiagBindFailure, devDiagUpstreamFailure, devDiagStartupCrash, api.CodeAppRuntimeOOM, api.CodeAppLoopbackBound, api.CodeAppArchMismatch, api.CodeAppHealthzUnauthorized:
		return d, true
	default:
		return devDiagnostic{}, false
	}
}

func renderDevDiagnostic(w io.Writer, d devDiagnostic) {
	if d.Catalog {
		PrintWarn(w, "developer diagnostic: code=%s phase=%s deployment=%s", d.Code, d.Phase, d.DeploymentID)
		if d.Hint != "" {
			PrintProgress(w, "next: %s", d.Hint)
		}
		if d.LogsCommand != "" {
			PrintProgress(w, "logs: %s", d.LogsCommand)
		}
		return
	}
	RenderTitle(w, d.Title)
	if d.Phase != "" {
		_, _ = fmt.Fprintf(w, "  phase: %s\n", d.Phase)
	}
	if d.Detail != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", d.Detail)
	}
	if d.Hint != "" {
		RenderHintRow(w, d.Hint)
	}
	if d.Why != "" {
		RenderWhyRow(w, d.Why)
	}
	if d.Fix != "" {
		RenderFixRow(w, d.Fix)
	}
	if len(d.Logs) > 0 {
		RenderRelevantLogs(w, d.Logs)
	}
	if d.LogsCommand != "" {
		PrintProgress(w, "logs: %s", d.LogsCommand)
	}
}
