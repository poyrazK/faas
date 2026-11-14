package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Recovery names the requested health scope, not a successful assessment.
// An omitted environment retains the default-scope route.
type appHealthWaitRecovery struct {
	App            string `json:"app"`
	Environment    string `json:"environment,omitempty"`
	InspectCommand string `json:"inspect_command"`
	ResumeCommand  string `json:"resume_command"`
}

func newAppHealthWaitRecovery(slug, environment string, timeout, interval time.Duration) *appHealthWaitRecovery {
	if (!api.ValidAppSlug(slug) && !api.ValidPreviewAppSlug(slug)) ||
		(environment != "" && !api.ValidProjectEnvironmentSlug(environment)) ||
		timeout <= 0 || interval < time.Second || interval > time.Hour {
		return nil
	}
	args := []string{"app", "health", "--app", slug}
	if environment != "" {
		args = append(args, "--env", environment)
	}
	inspect := savedNameSuggestionCommand(args)
	resume := savedNameSuggestionCommand(append(args, "--wait", "--timeout", timeout.String(), "--poll-interval", interval.String()))
	redact := errorTextRedactor()
	return &appHealthWaitRecovery{App: redact(slug), Environment: redact(environment),
		InspectCommand: redact(inspect), ResumeCommand: redact(resume)}
}

func renderAppHealthWaitProblem(problem api.Problem, code int, slug, environment string, timeout, interval time.Duration) int {
	_ = writeJSONWaitProblem(osStderr, diagnosticProblem(problem), code, newAppHealthWaitRecovery(slug, environment, timeout, interval))
	return code
}

func printAppHealthWaitError(title string, err error, slug, environment string, timeout, interval time.Duration) int {
	if jsonOutput {
		if errors.Is(err, context.Canceled) {
			return renderAppHealthWaitProblem(transportProblem(err), 130, slug, environment, timeout, interval)
		}
		var remote *APIError
		if errors.As(err, &remote) {
			return renderAppHealthWaitProblem(remote.Problem, exitCodeForStatus(remote.Problem.Status), slug, environment, timeout, interval)
		}
	}
	return printErr(title, err)
}
