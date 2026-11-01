package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// doctorAppEnv is the env-required check's view of the app's own runtime
// configuration: the names of its env vars and secrets in one scope. Values
// never leave the server; both list endpoints return names only.
type doctorAppEnv struct {
	// App is the slug doctor compared with, or tried to.
	App string
	// Names holds the env and secret names when the read succeeded.
	Names map[string]bool
	// Reason says why the names were not read; empty after a read.
	Reason string
}

func (e *doctorAppEnv) checked() bool { return e != nil && e.Reason == "" && e.Names != nil }

func (e *doctorAppEnv) uncheckedReason() string {
	if e == nil || e.Reason == "" {
		return "the app's env and secrets were not checked, so they may already be set"
	}
	return "the app's env and secrets were not checked (" + e.Reason + "), so they may already be set"
}

// doctorAppEnvTimeout bounds the two name reads so an unreachable API does
// not stall a local preflight.
const doctorAppEnvTimeout = 10 * time.Second

// loadDoctorAppEnv reads the env and secret names of the app doctor runs
// for: --app, else the linked project's app. Nothing is read when no app is
// known or the CLI is logged out; env-required then warns instead of
// failing, because its local view may be incomplete (CLI audit 2026-10-31).
func loadDoctorAppEnv(explicitApp, dir string) doctorAppEnv {
	app := explicitApp
	if app == "" {
		linked, err := linkedAppSlug(dir)
		if err != nil {
			if errors.Is(err, errProjectContextNotFound) {
				return doctorAppEnv{Reason: "no app is linked; pass --app or run `gregale link`"}
			}
			return doctorAppEnv{Reason: err.Error()}
		}
		app = linked
	}
	token := loadToken()
	if token == "" {
		return doctorAppEnv{App: app, Reason: "not logged in"}
	}
	scope, err := resolveEnvironmentFromContext("", dir)
	if err != nil {
		return doctorAppEnv{App: app, Reason: err.Error()}
	}
	scope = scopeOrDefault(scope)
	ctx, cancel := context.WithTimeout(context.Background(), doctorAppEnvTimeout)
	defer cancel()
	client := NewClient(apiBase(), token)
	secrets, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return doctorAppEnv{App: app, Reason: fmt.Sprintf("could not read %s's secrets: %v", app, errorTextRedactor()(err.Error()))}
	}
	env, err := client.GetAppsSlugEnvWithScope(ctx, app, scope)
	if err != nil {
		return doctorAppEnv{App: app, Reason: fmt.Sprintf("could not read %s's env: %v", app, errorTextRedactor()(err.Error()))}
	}
	names := map[string]bool{}
	for _, row := range secrets.Secrets {
		names[row.Key] = true
	}
	for _, row := range env.Env {
		names[row.Key] = true
	}
	return doctorAppEnv{App: app, Names: names}
}
