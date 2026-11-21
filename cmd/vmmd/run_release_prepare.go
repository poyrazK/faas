package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/runrelease"
)

// prepare emits a draft for operator review. It executes no referenced binary,
// accepts no signing/DB credentials and writes only to the supplied stdout.
func prepareRunReleasePlan(ctx context.Context, args []string, exe string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run-release prepare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source-commit", "", "exact source commit qualified by acceptance")
	reader := fs.String("candidate-reader", "", "candidate's previously emitted reader manifest")
	rollback := fs.String("rollback-binary", "", "verified compatible rollback vmmd")
	rollbackReader := fs.String("rollback-reader", "", "rollback binary's previously emitted reader manifest")
	cfg := fs.String("config", "", "candidate vmmd configuration")
	rbCfg := fs.String("rollback-config", "", "writer-disabled rollback configuration")
	acceptance := fs.String("acceptance", "", "reviewed native Run acceptance summary")
	paths := map[string]*string{}
	for _, name := range []string{"kernel", "base", "guest_executor", "firecracker", "jailer", "jail_helper"} {
		paths[name] = fs.String("artifact-"+name, "", "release artifact's absolute path")
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return adoptionExitError
	}
	plan := runrelease.Plan{Schema: runrelease.PlanSchema, SourceCommit: *source, Artifacts: map[string]runrelease.File{}}
	for _, item := range []struct {
		path string
		pin  *runrelease.File
	}{{exe, &plan.Candidate.File}, {*reader, &plan.Candidate.Reader}, {*rollback, &plan.Rollback.File}, {*rollbackReader, &plan.Rollback.Reader}, {*cfg, &plan.CandidateConfig}, {*rbCfg, &plan.RollbackConfig}, {*acceptance, &plan.Acceptance}} {
		digest, err := runrelease.Digest(ctx, item.path)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "run-release prepare: a required input cannot be hashed")
			return adoptionExitError
		}
		*item.pin = runrelease.File{Path: item.path, SHA256: digest}
	}
	for name, path := range paths {
		digest, err := runrelease.Digest(ctx, *path)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "run-release prepare: cannot hash %s artifact\n", name)
			return adoptionExitError
		}
		plan.Artifacts[name] = runrelease.File{Path: *path, SHA256: digest}
	}
	if err := plan.Validate(); err != nil {
		_, _ = fmt.Fprintln(stderr, "run-release prepare: source and absolute input paths are required")
		return adoptionExitError
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(plan); err != nil {
		return adoptionExitError
	}
	return adoptionExitOK
}
