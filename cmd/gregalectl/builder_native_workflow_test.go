package main

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// builderNativeJobQueries returns every jq filter builder-native.yml uses to
// read the images run's builder-base job conclusion.
func builderNativeJobQueries(t *testing.T) []string {
	t.Helper()
	workflow := readWorkflow(t, "builder-native.yml")
	queries := regexp.MustCompile(`--jq '([^']*builder-base[^']*)'`).FindAllStringSubmatch(workflow, -1)
	if len(queries) != 2 {
		t.Fatalf("builder-native.yml reads the builder-base conclusion in %d places, want 2 (workflow_run and manual)", len(queries))
	}
	out := make([]string, 0, len(queries))
	for _, q := range queries {
		out = append(out, q[1])
	}
	return out
}

// TestBuilderNativeReadsTheBuilderBaseConclusionWhetherOrNotTheJobRan pins the
// lookup to the job's name prefix. images.yml names the job with an
// expression; GitHub renders it only for a job that ran, and a skipped job
// keeps the raw `${{ ... }}` text. An exact-name lookup read every skip as a
// missing job, so builder-native failed on each push that did not rebuild
// builder-base (no success from 2026-11-08 to 2026-11-10).
func TestBuilderNativeReadsTheBuilderBaseConclusionWhetherOrNotTheJobRan(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq not installed")
	}
	cases := []struct {
		name string
		jobs string
		want string
	}{
		{
			name: "skipped job keeps the unrendered name",
			jobs: `{"jobs":[{"name":"runtime-change","conclusion":"success"},` +
				`{"name":"builder-base (${{ (((github.event_name == 'pull_request') && 'linux/amd64') || 'linux/amd64 + linux/arm64') }})","conclusion":"skipped"}]}`,
			want: "skipped",
		},
		{
			name: "main push that ran the job",
			jobs: `{"jobs":[{"name":"builder-base (linux/amd64 + linux/arm64)","conclusion":"success"}]}`,
			want: "success",
		},
		{
			name: "pull request build name",
			jobs: `{"jobs":[{"name":"builder-base (linux/amd64)","conclusion":"failure"}]}`,
			want: "failure",
		},
		{
			name: "other jobs never match",
			jobs: `{"jobs":[{"name":"builder-base-scan","conclusion":"success"},{"name":"runtime-base (linux/amd64)","conclusion":"success"}]}`,
			want: "",
		},
	}
	for _, query := range builderNativeJobQueries(t) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				cmd := exec.Command(jq, "-r", query)
				cmd.Stdin = strings.NewReader(tc.jobs)
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("jq %q: %v", query, err)
				}
				if got := strings.TrimSpace(string(out)); got != tc.want {
					t.Fatalf("jq %q = %q, want %q", query, got, tc.want)
				}
			})
		}
	}
}
