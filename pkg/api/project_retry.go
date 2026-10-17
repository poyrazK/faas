package api

import (
	"context"
	"encoding/json"
)

// ProjectRetryHeader carries app IDs and the failed deployment IDs observed by
// the caller. Servers must validate ownership and current status before forcing
// an unchanged workload to rebuild.
const ProjectRetryHeader = "Project-Retry-Targets"

type projectRetryContextKey struct{}

func ContextWithProjectRetry(ctx context.Context, targets map[string]string) context.Context {
	copyTargets := make(map[string]string, len(targets))
	for app, deployment := range targets {
		copyTargets[app] = deployment
	}
	return context.WithValue(ctx, projectRetryContextKey{}, copyTargets)
}
func projectRetryHeader(ctx context.Context) string {
	targets, _ := ctx.Value(projectRetryContextKey{}).(map[string]string)
	if len(targets) == 0 {
		return ""
	}
	value, _ := json.Marshal(targets)
	return string(value)
}
