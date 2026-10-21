package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildLogExcerptTimeout bounds the build-log read a failed deploy makes to
// show the failing command's output.
const buildLogExcerptTimeout = 10 * time.Second

// buildkitPrefix is BuildKit's "#13 1.778 " or "#13 " step prefix, or the
// bare "1.778 " one it uses when it repeats a failed step's output.
var buildkitPrefix = regexp.MustCompile(`^(#\d+ (\d+\.\d+ )?|\d+\.\d+ )`)

// buildStageFailure reports a deployment that failed while building its
// image, where the cause is in the build log rather than the error string.
func buildStageFailure(d api.DeploymentResponse) bool {
	return d.ErrorCode == api.CodeDepInstallFailed || d.Error == "user_error" || strings.HasPrefix(d.Error, "build ")
}

// buildErrorExcerpt returns up to eight lines starting at the first
// package-manager or compiler error in a build log, without BuildKit's step
// prefixes, its own stack traces and debug lines, and the repeat BuildKit
// prints when it summarizes the failed step. A failed npm install showed only
// "Deploy failed: build exited 1"; "npm error 404 Not Found - GET …" was 30
// lines into a separate command's output (H10-74).
func buildErrorExcerpt(lines []string) []string {
	var cleaned []string
	seen := map[string]bool{}
	for _, raw := range lines {
		line := strings.TrimSpace(buildkitPrefix.ReplaceAllString(strings.TrimSpace(raw), ""))
		if line == "" || seen[line] || buildLogNoise(line) {
			continue
		}
		seen[line] = true
		cleaned = append(cleaned, line)
	}
	for i, line := range cleaned {
		if buildErrorLine(line) {
			return cleaned[i:min(i+8, len(cleaned))]
		}
	}
	return nil
}

func buildErrorLine(line string) bool {
	for _, marker := range []string{"npm error", "npm ERR!", "ERROR:", "error:", "Error:", "error[", "fatal:"} {
		if strings.HasPrefix(line, marker) || strings.Contains(line, " "+marker) {
			return true
		}
	}
	return false
}

func buildLogNoise(line string) bool {
	return strings.HasPrefix(line, "time=\"") || strings.HasPrefix(line, "[buildkitd]") ||
		strings.HasPrefix(line, "github.com/") || strings.HasPrefix(line, "runtime.") ||
		strings.Contains(line, ".go:") || strings.HasPrefix(line, ".buildkit ") ||
		strings.HasPrefix(line, "------") || strings.HasPrefix(line, "FAILED:") ||
		strings.HasPrefix(line, "deployment ") || strings.Contains(line, "A complete log of this run can be found")
}

// printBuildFailureExcerpt adds the failing command's output and the full-log
// command after a build-stage deploy failure. A log read failure prints only
// the command.
func printBuildFailureExcerpt(ctx context.Context, c *Client, w io.Writer, slug string, d api.DeploymentResponse) {
	if jsonOutput || !buildStageFailure(d) || slug == "" || d.ID == "" {
		return
	}
	if c != nil {
		readCtx, cancel := context.WithTimeout(ctx, buildLogExcerptTimeout)
		defer cancel()
		if excerpt := buildErrorExcerpt(readBuildLog(readCtx, c, d.ID)); len(excerpt) > 0 {
			_, _ = fmt.Fprintln(w, "  build output:")
			for _, line := range excerpt {
				_, _ = fmt.Fprintf(w, "    %s\n", line)
			}
		}
	}
	_, _ = fmt.Fprintf(w, "  full build log: gregale logs %s --deployment %s\n", slug, d.ID)
}

// readBuildLog collects a finished deployment's build log lines, the stream
// gregale logs --deployment reads for a deployment that never served.
func readBuildLog(ctx context.Context, c *Client, deploymentID string) []string {
	body, err := c.StreamDeploymentLogs(ctx, deploymentID, nil, 500, false)
	if err != nil {
		return nil
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
	var lines []string
	for {
		select {
		case <-ctx.Done():
			return lines
		case e, ok := <-dec.Events():
			if !ok || e.Event == "end" {
				return lines
			}
			var entry struct {
				Line string `json:"line"`
			}
			if e.Event == "log" && json.Unmarshal([]byte(e.Data), &entry) == nil && entry.Line != "" && len(lines) < 5000 {
				lines = append(lines, strings.Split(entry.Line, "\n")...)
			}
		}
	}
}
