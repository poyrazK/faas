package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func cmdProjectsReleaseStage(args []string) int {
	action := args[0]
	flags, positional := splitArgsForFlags(args[1:])
	fs := newFlagSet("projects-environments-release-sets-"+action, flag.ContinueOnError)
	file := fs.String("file", "", "prepared release manifest JSON file")
	stageID := fs.String("stage", "", "stage UUID")
	ttl := fs.Int("ttl-seconds", api.ProjectReleaseStageMaxTTLSeconds, "temporary billable service capacity lease")
	if err := fs.Parse(flags); err != nil || fs.NArg() != 0 || len(positional) != 2 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) ||
		(action == "stage" && (*file == "" || *stageID != "" || *ttl < 1 || *ttl > api.ProjectReleaseStageMaxTTLSeconds)) || (action != "stage" && (*file != "" || *stageID == "")) {
		PrintUsage(osStderr, "usage: gregale projects environments release-sets <stage|stage-status|cancel-stage> <project> <environment> [--file MANIFEST --ttl-seconds 1800 | --stage UUID] [--json]", "projects environments")
		return 1
	}
	if action != "stage" {
		ignoredTTL := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "ttl-seconds" {
				ignoredTTL = true
			}
		})
		if ignoredTTL {
			return printErr("Invalid staging flags", fmt.Errorf("--ttl-seconds only applies to stage"))
		}
		if parsed, err := uuid.Parse(*stageID); err != nil || parsed == uuid.Nil || parsed.String() != *stageID {
			return printErr("Invalid stage", fmt.Errorf("stage must be a canonical UUID"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out api.ProjectReleaseStage
	switch action {
	case "stage":
		var manifest api.CheckedProjectReleaseManifest
		if err := readCheckedReleaseFile(*file, &manifest); err != nil {
			return printErr("Invalid prepared release manifest", err)
		}
		if manifest.Project != positional[0] || manifest.Environment != positional[1] || manifest.Deactivate {
			return printErr("Invalid staging manifest", fmt.Errorf("use a matching forward or rollback manifest"))
		}
		out, err = client.StageCheckedProjectRelease(ctx, positional[0], positional[1], api.StageProjectReleaseRequest{Manifest: manifest, TTLSeconds: *ttl})
	case "stage-status":
		out, err = client.GetProjectReleaseStage(ctx, positional[0], positional[1], *stageID)
	case "cancel-stage":
		out, err = client.CancelProjectReleaseStage(ctx, positional[0], positional[1], *stageID)
	}
	if err != nil {
		return printErr("Release staging failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if _, err := fmt.Fprintf(osStdout, "Stage %s: %s; expires %s\n", out.ID, out.Status, out.ExpiresAt.Format(time.RFC3339)); err != nil {
		return printErr("Could not write staging status", err)
	}
	if out.Reason != "" {
		if _, err := fmt.Fprintln(osStdout, out.Reason); err != nil {
			return printErr("Could not write staging status", err)
		}
	}
	for _, member := range out.Members {
		if _, err := fmt.Fprintf(osStdout, "%s %s: %d/%d ready\n", member.App, member.DeploymentID, member.Ready, member.Desired); err != nil {
			return printErr("Could not write staging status", err)
		}
	}
	return 0
}
