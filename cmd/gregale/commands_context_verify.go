package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type contextVerification struct {
	OK             bool                 `json:"ok"`
	ExitCode       int                  `json:"exit_code"`
	Account        *profileCheckAccount `json:"account,omitempty"`
	Authentication string               `json:"authentication"`
	Project        string               `json:"project"`
	App            string               `json:"app"`
	Environment    string               `json:"environment"`
	Message        string               `json:"message"`
	NextCommand    string               `json:"next_command,omitempty"`
}

func verifyProjectConnection(link localProjectContext, linked bool, timeout time.Duration) *contextVerification {
	report := &contextVerification{Project: "not_checked", App: "not_checked", Environment: "not_checked"}
	connection := effectiveConnectionContext()
	token := loadToken()
	source := "profile:" + connection.Profile
	if os.Getenv("FAAS_TOKEN") != "" {
		source = "env:FAAS_TOKEN"
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	client := NewClient(connection.APIBase, token)
	client.SetCompletionCache(nil)
	account := checkProfileConnection(ctx, client, connection, source, token != "")
	report.Account, report.Authentication = account.Account, account.Authentication
	if !account.OK {
		report.ExitCode = account.ExitCode
		report.Message = "The selected connection could not be authenticated."
		if account.Diagnostic != nil {
			report.Message = account.Diagnostic.Message
		}
		report.NextCommand = recoveryCLI() + " config profile check"
		return report
	}
	fail := func(message, next string) *contextVerification {
		report.ExitCode = 1
		report.Message = message
		report.NextCommand = next
		if ctx.Err() == context.Canceled {
			report.ExitCode = 130
		}
		if ctx.Err() == context.DeadlineExceeded {
			report.ExitCode = 124
		}
		return report
	}
	if !linked {
		return fail("Account authenticated, but this checkout has no linked project.", recoveryCLI()+" project list")
	}
	if link.Project == "" {
		report.Project, report.Environment = "not_set_standalone_app", "not_set_command_defaults"
		if err := verifyStandaloneContextApp(ctx, client, link.App); err != nil {
			report.App = "unavailable"
			return fail(err.Error(), recoveryCLI()+" app list")
		}
		report.App, report.OK = "verified", true
		report.Message = "Account and saved standalone app were verified on the selected connection; health was not checked."
		return report
	}
	project, err := client.GetProject(ctx, link.Project)
	if err != nil {
		report.Project = "unavailable"
		return fail("The linked project could not be verified on this connection.", recoveryCLI()+" project list")
	}
	if project.Slug != link.Project {
		report.Project = "identity_mismatch"
		return fail("Project response did not identify the linked project.", recoveryCLI()+" project list")
	}
	report.Project = "verified"
	if link.App == "" {
		report.App = "not_set"
	} else {
		report.App = "not_in_project"
		for _, workload := range project.Workloads {
			if workload.Slug == link.App {
				report.App = "verified"
				break
			}
		}
	}
	if link.Environment == "" {
		report.Environment = "not_set_command_defaults"
	} else {
		environment, err := client.GetProjectEnvironment(ctx, link.Project, link.Environment)
		if err != nil {
			report.Environment = "unavailable"
			return fail("The linked environment could not be verified on this connection.", recoveryCLI()+" environment list "+recoveryArgument(link.Project))
		}
		if environment.Slug != link.Environment || project.ID == "" || environment.ProjectID != project.ID {
			report.Environment = "identity_mismatch"
			return fail("Environment response did not match the linked project and environment.", recoveryCLI()+" environment list "+recoveryArgument(link.Project))
		}
		report.Environment = "verified"
	}
	if report.App != "verified" {
		return fail("The default app is missing or is not a workload in the linked project.", recoveryCLI()+" project get "+recoveryArgument(link.Project))
	}
	report.OK = true
	report.Message = "Account and configured linked targets were verified on the selected connection. Unset environments retain command-specific defaults; health and configuration delivery are not checked."
	return report
}

func verifyStandaloneContextApp(ctx context.Context, client *Client, slug string) error {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("verify saved app: %w", err)
	}
	if app.Slug != slug {
		return errors.New("app response did not identify the saved app")
	}
	return nil
}

func renderContextVerification(report *contextVerification) {
	_, _ = fmt.Fprintf(osStdout, "Connection verification: %s\n", report.Message)
	if report.Account != nil {
		_, _ = fmt.Fprintf(osStdout, "Authenticated account: %s; ID=%s; plan=%s\n", oneLine(report.Account.Email), oneLine(report.Account.ID), oneLine(report.Account.Plan))
	}
	_, _ = fmt.Fprintf(osStdout, "Project: %s; app: %s; environment: %s\n", report.Project, report.App, report.Environment)
	if report.NextCommand != "" {
		_, _ = fmt.Fprintf(osStdout, "Next step (POSIX shells):\n  %s\n", report.NextCommand)
	}
}

func validateContextVerifyEndpoint() error {
	if _, err := validateConfigAPIBase(apiBase()); err != nil {
		return errors.New("API address must be http(s) without credentials, query parameters, or fragments; FAAS_API overrides profile settings")
	}
	return nil
}
