//go:build !linux

package main

import "log/slog"

func startEventPublishProxy(_ *slog.Logger) error { return nil }

var _ = startEventPublishProxy

func StampEventPublishEnv(env []string) []string { return env }

func StampRuntimeConfigEnv(env []string) []string { return env }
