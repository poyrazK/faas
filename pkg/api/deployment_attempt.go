package api

import (
	"context"
	"io"
	"net/http"
	"strings"
)

type singleDeploymentAttemptKey struct{}

func singleDeploymentAttemptContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, singleDeploymentAttemptKey{}, true)
}

// net/http can replay keyed requests on a reused connection without returning
// the first transport error. Deployment callers must see each uncertain
// attempt, otherwise a later rejection can conceal an earlier acceptance.
func preventDeploymentReplay(req *http.Request) {
	if single, _ := req.Context().Value(singleDeploymentAttemptKey{}).(bool); !single && req.Header.Get(DeploymentAttemptHeader) == "" {
		return
	}
	req.GetBody = nil
	if req.Body == nil || req.Body == http.NoBody {
		// A bodyless keyed POST is replayable even without GetBody. An empty
		// JSON object is ignored by upload commit and makes it non-replayable.
		req.Body = io.NopCloser(strings.NewReader("{}"))
		req.ContentLength = 2
		req.Header.Set("Content-Type", "application/json")
	}
}

type deploymentBoundaryCallbackKey struct{}
type deploymentBoundaryRequestKey struct{}

// ContextWithDeploymentSubmissionBoundary installs a durable checkpoint before
// deployment-creating network sends. A callback failure prevents submission.
func ContextWithDeploymentSubmissionBoundary(ctx context.Context, beforeSend func() error) context.Context {
	return context.WithValue(ctx, deploymentBoundaryCallbackKey{}, beforeSend)
}

func deploymentSubmissionContext(ctx context.Context) context.Context {
	return context.WithValue(singleDeploymentAttemptContext(ctx), deploymentBoundaryRequestKey{}, true)
}

func recordDeploymentSubmissionBoundary(req *http.Request) error {
	if submission, _ := req.Context().Value(deploymentBoundaryRequestKey{}).(bool); !submission {
		return nil
	}
	callback, _ := req.Context().Value(deploymentBoundaryCallbackKey{}).(func() error)
	if callback != nil {
		return callback()
	}
	return nil
}
