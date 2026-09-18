//go:build !linux

package main

import "log/slog"

const EventPublishEndpoint = "http://169.254.169.254/v1/events:publish"

func startEventPublishProxy(_ *slog.Logger) error { return nil }

func StampEventPublishEnv(env []string) []string { return env }

func StampRuntimeConfigEnv(env []string) []string { return env }
