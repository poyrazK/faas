package main_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type releaseTaskReader struct {
	slug string
	task api.AppTaskResponse
	err  error
}

func (r *releaseTaskReader) GetAppTask(_ context.Context, slug, _ string) (api.AppTaskResponse, error) {
	r.slug = slug
	return r.task, r.err
}

// `gregale deploys status <id>` has no app slug; imaged's detail names it.
// A receipt that cannot be read says so instead of printing nothing (MCP
// Tasks prod probe 2026-11-22: the route answered 501). A UUID in the path is
// the app ID imaged fell back to, not a slug.
func TestReleaseTaskOutputReadsSlugFromDetailAndReportsReadErrors(t *testing.T) {
	const task = "2f466e08-24ed-445e-8699-49567888210e"
	detail := func(app string) api.DeploymentResponse {
		return api.DeploymentResponse{Error: "release task " + task + " ended with status failed: command_failed: command exited unsuccessfully (exit code 127; the task's captured output is in its receipt, GET /v1/apps/" + app + "/tasks/" + task + ")"}
	}
	reader := &releaseTaskReader{task: api.AppTaskResponse{StderrTail: "sh: 1: npm: not found\n"}}
	var out bytes.Buffer
	main.PrintReleaseTaskOutput(context.Background(), reader, &out, "", detail("mcpt-web"))
	if reader.slug != "mcpt-web" || !strings.Contains(out.String(), "stderr: sh: 1: npm: not found") {
		t.Fatalf("read %s, printed %q", reader.slug, out.String())
	}

	failing := &releaseTaskReader{err: errors.New("Not implemented")}
	out.Reset()
	main.PrintReleaseTaskOutput(context.Background(), failing, &out, "", detail("mcpt-web"))
	if !strings.Contains(out.String(), "unavailable: Not implemented") {
		t.Fatalf("printed %q, want the read error", out.String())
	}

	byID := &releaseTaskReader{}
	out.Reset()
	main.PrintReleaseTaskOutput(context.Background(), byID, &out, "", detail("6f1c2b3a-1111-4222-8333-444455556666"))
	if byID.slug != "" || out.Len() != 0 {
		t.Fatalf("read app %q and printed %q for an app ID", byID.slug, out.String())
	}
}
