package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeAppTaskReader struct {
	slug, id string
	task     api.AppTaskResponse
	err      error
}

func (f *fakeAppTaskReader) GetAppTask(_ context.Context, slug, id string) (api.AppTaskResponse, error) {
	f.slug, f.id = slug, id
	return f.task, f.err
}

// prod 2026-11-19: a failed migration showed "exit code 4" and a raw API path
// with a literal {slug}; the output was nowhere in the CLI.
func TestFailedDeployPrintsReleaseCommandOutput(t *testing.T) {
	reader := &fakeAppTaskReader{task: api.AppTaskResponse{
		StdoutTail: "about to fail\n",
		StderrTail: "MIGRATION_BOOM: column foo does not exist\n",
	}}
	d := api.DeploymentResponse{Error: "release task 2f466e08-24ed-445e-8699-49567888210e ended with status failed: command_failed: command exited unsuccessfully (exit code 4; ...)"}
	var out bytes.Buffer
	printReleaseTaskOutput(context.Background(), reader, &out, "bh2-svc", d)
	if reader.slug != "bh2-svc" || reader.id != "2f466e08-24ed-445e-8699-49567888210e" {
		t.Fatalf("read task %s/%s", reader.slug, reader.id)
	}
	for _, want := range []string{"output from your release command", "stdout: about to fail", "stderr: MIGRATION_BOOM: column foo does not exist"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output %q lacks %q", out.String(), want)
		}
	}
}

func TestOtherDeployFailuresReadNoTask(t *testing.T) {
	reader := &fakeAppTaskReader{}
	var out bytes.Buffer
	printReleaseTaskOutput(context.Background(), reader, &out, "app", api.DeploymentResponse{Error: "build exited 1"})
	if reader.id != "" || out.Len() != 0 {
		t.Fatalf("read task %q, printed %q; want nothing for a build failure", reader.id, out.String())
	}
}

// `gregale deploys status <id>` has no app slug; imaged's detail names it.
// A receipt that cannot be read says so instead of printing nothing (MCP
// Tasks prod probe 2026-11-22: the route answered 501).
func TestReleaseTaskOutputReadsSlugFromDetailAndReportsReadErrors(t *testing.T) {
	d := api.DeploymentResponse{Error: "release task 2f466e08-24ed-445e-8699-49567888210e ended with status failed: command_failed: command exited unsuccessfully (exit code 127; the task's captured output is in its receipt, GET /v1/apps/mcpt-web/tasks/2f466e08-24ed-445e-8699-49567888210e)"}
	reader := &fakeAppTaskReader{task: api.AppTaskResponse{StderrTail: "sh: 1: npm: not found\n"}}
	var out bytes.Buffer
	printReleaseTaskOutput(context.Background(), reader, &out, "", d)
	if reader.slug != "mcpt-web" || !strings.Contains(out.String(), "stderr: sh: 1: npm: not found") {
		t.Fatalf("read %s, printed %q", reader.slug, out.String())
	}
	failing := &fakeAppTaskReader{err: errors.New("Not implemented")}
	out.Reset()
	printReleaseTaskOutput(context.Background(), failing, &out, "", d)
	if !strings.Contains(out.String(), "unavailable: Not implemented") {
		t.Fatalf("printed %q, want the read error", out.String())
	}
}
