package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const mcpTaskFenceEnv = "GREGALE_TASK_EXECUTION_FENCE"
const mcpTaskFenceAPIEnv = "GREGALE_TASK_EXECUTION_API"

type mcpTaskFenceTransport struct {
	next                     http.RoundTripper
	intent, operation, token string
	origin                   *url.URL
}

func (t mcpTaskFenceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !strings.HasPrefix(r.URL.Path, "/v1/task-tool-release-intents/") {
		if t.origin == nil || r.URL.Scheme != t.origin.Scheme || !strings.EqualFold(r.URL.Host, t.origin.Host) {
			return nil, errors.New("Task executor request left its native API origin")
		}
		r = r.Clone(r.Context())
		r.Header.Set(api.TaskExecutionIntentHeader, t.intent)
		r.Header.Set(api.TaskExecutionOperationHeader, t.operation)
		r.Header.Set(api.TaskExecutionClaimHeader, t.token)
	}
	return t.next.RoundTrip(r)
}
func withMCPTaskFence(c *api.Client) *api.Client {
	value := os.Getenv(mcpTaskFenceEnv)
	if value == "" {
		return c
	}
	parts := strings.Split(value, ":")
	// An invalid inherited fence must still reach the server as a refusing fence.
	if len(parts) != 3 {
		parts = []string{"invalid", "invalid", "invalid"}
	}
	origin, err := url.Parse(os.Getenv(mcpTaskFenceAPIEnv))
	if err != nil || origin.Host == "" || origin.User != nil {
		origin = nil
	}
	return c.WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		return mcpTaskFenceTransport{next, parts[0], parts[1], parts[2], origin}
	})
}

// Called only after the synchronous adapter has returned and its children have
// exited normally. An interrupted gate cannot attest that external SQL settled.
func exitMCPTaskExecutor(c *Client, record api.TaskToolReleaseIntentRecord, p mcpTaskExecutionProfile, token string, settled bool) {
	if !settled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = c.CheckTaskToolReleaseIntent(ctx, record.ID, api.TaskToolReleaseIntentCheck{ReleaseFingerprint: record.Intent.ReleaseFingerprint,
		Execution: &api.TaskToolReleaseExecutionRequest{Action: "executor_exited", OperationID: p.OperationID, BindingsSHA256: p.digest(), ClaimToken: token}})
}
