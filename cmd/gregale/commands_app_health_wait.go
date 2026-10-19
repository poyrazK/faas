package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func appHealthFresh(out api.AppHealthResponse, now time.Time) bool {
	at, err := time.Parse(time.RFC3339Nano, out.EvaluatedAt)
	age := now.Sub(at)
	return err == nil && out.ValidForSeconds > 0 && age >= 0 && age.Seconds() < float64(out.ValidForSeconds)
}

func appHealthCheckPassed(out api.AppHealthResponse, environment string, now time.Time) bool {
	return out.Status == "healthy" && out.Scope == appHealthScope(environment) && appHealthFresh(out, now)
}

// appHealthScope is the scope an assessment must report: the requested
// project environment, or the default scope when none was named.
func appHealthScope(environment string) string {
	if environment != "" {
		return environment
	}
	return api.DefaultEnvScope
}

// readAppHealth reads the default scope, or a project environment. A project
// app's hostname can serve an environment while an older default-scope
// release stays live, which made the default assessment idle (H10-113).
func readAppHealth(ctx context.Context, client *api.Client, slug, environment string) (api.AppHealthResponse, error) {
	if environment != "" {
		return client.GetAppHealthInEnvironment(ctx, slug, environment)
	}
	return client.GetAppHealth(ctx, slug)
}

func waitAppHealth(client *api.Client, slug, environment string, timeout, interval time.Duration) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	var last *api.AppHealthResponse
	previous := ""
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Waiting for a fresh healthy %s-scope assessment from recorded evidence; this does not wake or restart the app.\n", appHealthScope(environment))
	}
	for ctx.Err() == nil {
		readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
		out, err := readAppHealth(readCtx, client, slug, environment)
		readCancel()
		if ctx.Err() != nil {
			break
		}
		progress := "Health assessment unavailable; retrying."
		if err != nil {
			if inspectWatchPermanentError(err) {
				return printErr("Health wait failed", err)
			}
		} else {
			last = &out
			fresh := appHealthFresh(out, time.Now())
			if appHealthCheckPassed(out, environment, time.Now()) {
				return renderAppHealth(slug, out)
			}
			progress = fmt.Sprintf("Health: %s (%s); scope=%s; fresh=%t — %s", compactInspectFailureText(out.Status), compactInspectFailureText(out.Phase), compactInspectFailureText(out.Scope), fresh, compactInspectFailureText(out.Summary))
		}
		if !jsonOutput && progress != previous {
			if _, err := fmt.Fprintln(osStderr, progress); err != nil {
				return printErr("Could not write health progress", err)
			}
		}
		previous = progress
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return printErr("Health wait canceled", ctx.Err())
	}
	if last != nil {
		if code := renderAppHealth(slug, *last); code != 0 {
			return code
		}
	}
	message := fmt.Sprintf("Health wait timed out without a fresh healthy %s-scope assessment; any assessment shown is the last observation and may be stale.", appHealthScope(environment))
	if jsonOutput {
		_ = writeJSONProblemWithExit(os.Stderr, api.Problem{Type: docsSiteURL + "/errors/invalid-request", Title: "Health wait timed out", Status: 408, Detail: message, DocsURL: cliDocsURL}, 124)
	} else {
		_, _ = fmt.Fprintln(osStderr, message)
	}
	return 124
}
