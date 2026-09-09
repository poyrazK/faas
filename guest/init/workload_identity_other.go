//go:build !linux

package main

import "log/slog"

const workloadIdentityEndpoint = "http://127.0.0.1:2773/oidc/token"

func startWorkloadIdentityProxy(*slog.Logger) error { return nil }

func StampWorkloadIdentityEnv(env []string) []string { return env }
