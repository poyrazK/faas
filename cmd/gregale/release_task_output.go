package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// releaseTaskFailure matches imaged's release-command failure detail.
var releaseTaskFailure = regexp.MustCompile(`release task ([0-9a-fA-F-]{36}) ended with status`)

type appTaskReader interface {
	GetAppTask(ctx context.Context, slug, id string) (api.AppTaskResponse, error)
}

// printReleaseTaskOutput shows the captured output of a failed release
// command. The deployment error deliberately leaves it out (it reaches
// notifications), and the CLI had no command that read the task receipt, so
// a failed migration on prod 2026-11-19 showed only "exit code 4" and a raw
// API path.
func printReleaseTaskOutput(ctx context.Context, c appTaskReader, w io.Writer, slug string, d api.DeploymentResponse) {
	if c == nil || slug == "" {
		return
	}
	match := releaseTaskFailure.FindStringSubmatch(d.Error)
	if match == nil {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	task, err := c.GetAppTask(readCtx, slug, match[1])
	if err != nil {
		return
	}
	stdout, stderr := strings.TrimRight(task.StdoutTail, "\n"), strings.TrimRight(task.StderrTail, "\n")
	if stdout == "" && stderr == "" {
		return
	}
	_, _ = fmt.Fprintln(w, "  output from your release command:")
	for _, line := range releaseOutputLines(stdout, stderr) {
		_, _ = fmt.Fprintf(w, "    %s\n", line)
	}
	if task.OutputTruncated {
		_, _ = fmt.Fprintln(w, "    (earlier output was truncated)")
	}
}

func releaseOutputLines(stdout, stderr string) []string {
	const keep = 20
	var lines []string
	for _, part := range []struct{ label, text string }{{"stdout", stdout}, {"stderr", stderr}} {
		if part.text == "" {
			continue
		}
		all := strings.Split(part.text, "\n")
		if len(all) > keep {
			all = all[len(all)-keep:]
		}
		for _, line := range all {
			lines = append(lines, part.label+": "+line)
		}
	}
	return lines
}
