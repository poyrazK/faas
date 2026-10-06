package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjectReleaseWrite(args []string, publish bool) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("projects-environments-release-sets", flag.ContinueOnError)
	file := fs.String("file", "", "JSON file with ttl_seconds and exact workload deployment UUIDs")
	expected := fs.String("expected-active", "", "current release UUID, or none when no graph is active")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) || *file == "" || *expected == "" {
		return printErr("Invalid release selection", fmt.Errorf("use release-sets check|publish PROJECT ENVIRONMENT --file FILE --expected-active UUID|none"))
	}
	previous := ""
	if *expected != "none" {
		id, err := uuid.Parse(*expected)
		if err != nil || id == uuid.Nil {
			return printErr("Invalid active release", fmt.Errorf("expected-active must be a release UUID or none"))
		}
		previous = id.String()
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		return printErr("Could not read release file", err)
	}
	var req api.PublishProjectReleaseSetRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return printErr("Invalid release file", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return printErr("Invalid release file", fmt.Errorf("file must contain exactly one JSON object"))
	}
	if req.ExpectedActiveReleaseID != nil && *req.ExpectedActiveReleaseID != previous {
		return printErr("Conflicting release selection", fmt.Errorf("file and --expected-active disagree"))
	}
	req.ExpectedActiveReleaseID = &previous
	if req.TTLSeconds < 1 || req.TTLSeconds > api.RevisionPinMaxTTLSeconds || len(req.Deployments) < 1 || len(req.Deployments) > api.ProjectReleaseSetMaxMembers {
		return printErr("Invalid release file", fmt.Errorf("select every workload and a valid revision retention TTL"))
	}
	for slug, raw := range req.Deployments {
		id, err := uuid.Parse(raw)
		if !api.ValidAppSlug(slug) || err != nil || id == uuid.Nil {
			return printErr("Invalid release member", fmt.Errorf("%s requires an exact deployment UUID", slug))
		}
		req.Deployments[slug] = id.String()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !publish {
		report, err := client.CheckProjectReleaseSet(context.Background(), positional[0], positional[1], req)
		if err != nil {
			return printErr("Could not check project release", err)
		}
		if err := validateProjectReleaseCheck(report, req, positional[1]); err != nil {
			return printErr("Invalid project release check", err)
		}
		if jsonOutput {
			if err := writeJSON(report); err != nil {
				return jsonOut(err)
			}
		} else {
			renderProjectReleaseCheck(report)
		}
		if !report.Passed {
			return 1
		}
		return 0
	}
	release, err := client.PublishProjectReleaseSet(context.Background(), positional[0], positional[1], req)
	if err != nil {
		return printErr("Could not publish project release", err)
	}
	id, parseErr := uuid.Parse(release.ID)
	if parseErr != nil || id == uuid.Nil || !release.Active || release.Environment != positional[1] || release.TTLSeconds != req.TTLSeconds || release.BindingsCheck == nil || !release.BindingsCheck.Passed {
		return printErr("Invalid project release receipt", fmt.Errorf("server did not confirm checked activation of the selected graph"))
	}
	report := *release.BindingsCheck
	if err := validateProjectReleaseCheck(report, req, positional[1]); err != nil {
		return printErr("Invalid project release receipt", err)
	}
	if release.ProjectID != report.ProjectID || !sameProjectReleaseMembers(release.Members, report.Members) {
		return printErr("Invalid project release receipt", fmt.Errorf("activated membership differs from the checked graph"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(release))
	}
	_, _ = fmt.Fprintf(osStdout, "Activated release %s for %s/%s\n", release.ID, positional[0], positional[1])
	renderProjectReleaseCheck(report)
	return 0
}

func validateProjectReleaseCheck(report api.ProjectReleaseCheckResponse, req api.PublishProjectReleaseSetRequest, environment string) error {
	if report.Environment != environment || req.ExpectedActiveReleaseID == nil || report.ExpectedActiveReleaseID != *req.ExpectedActiveReleaseID || report.TTLSeconds != req.TTLSeconds || report.ProjectID == "" || report.GraphDigest != api.ProjectReleaseGraphDigest(report) || report.CheckedAt.IsZero() || len(report.Members) != len(req.Deployments) {
		return fmt.Errorf("check does not match the selected graph")
	}
	if !report.Passed {
		return nil
	}
	if len(report.Checks) != len(req.Deployments) || len(report.Blockers) > 0 {
		return fmt.Errorf("check omitted member evidence")
	}
	members := map[string]bool{}
	apps := map[string]bool{}
	for _, member := range report.Members {
		if members[canonicalGraphCLIUUID(member.DeploymentID)] || member.AppID == "" || apps[member.AppID] {
			return fmt.Errorf("check contains duplicate members")
		}
		members[canonicalGraphCLIUUID(member.DeploymentID)] = true
		apps[member.AppID] = true
	}
	checked := map[string]bool{}
	for _, check := range report.Checks {
		if !check.Passed || check.Scope != environment || req.Deployments[check.App] != canonicalGraphCLIUUID(check.DeploymentID) || !members[canonicalGraphCLIUUID(check.DeploymentID)] || checked[check.App] {
			return fmt.Errorf("check does not confirm every exact deployment")
		}
		checked[check.App] = true
	}
	return nil
}

func sameProjectReleaseMembers(a, b []api.ProjectReleaseSetMemberResponse) bool {
	if len(a) != len(b) {
		return false
	}
	expected := map[string]string{}
	for _, member := range a {
		if expected[canonicalGraphCLIUUID(member.AppID)] != "" {
			return false
		}
		expected[canonicalGraphCLIUUID(member.AppID)] = canonicalGraphCLIUUID(member.DeploymentID)
	}
	for _, member := range b {
		if expected[canonicalGraphCLIUUID(member.AppID)] != canonicalGraphCLIUUID(member.DeploymentID) {
			return false
		}
		delete(expected, canonicalGraphCLIUUID(member.AppID))
	}
	return len(expected) == 0
}

func renderProjectReleaseCheck(report api.ProjectReleaseCheckResponse) {
	_, _ = fmt.Fprintf(osStdout, "Graph %s: passed=%t (%d workloads)\n", report.GraphDigest, report.Passed, len(report.Members))
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(osStdout, "  %s %s passed=%t\n", check.App, check.DeploymentID, check.Passed)
	}
	for _, blocker := range report.Blockers {
		_, _ = fmt.Fprintf(osStdout, "  %s %s: %s\n", blocker.DeploymentID, blocker.Code, blocker.Message)
	}
}

func canonicalGraphCLIUUID(raw string) string {
	if id, err := uuid.Parse(raw); err == nil {
		return id.String()
	}
	return raw
}
