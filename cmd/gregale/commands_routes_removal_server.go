package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// policy and server-check read by default; authorize is an explicit persisted
// admin approval. The existing approve command remains a local attestation.
func cmdRouteRemovalServer(name string, args []string) int {
	fs := newFlagSet("routes migration "+name, flag.ContinueOnError)
	app := fs.String("app", "", "production app slug")
	var mode, grace, age, baseline, candidate, readiness, mapping, output *string
	var revision *int64
	var acknowledge, fail *bool
	switch name {
	case "policy":
		mode = fs.String("mode", "", "set report or enforce; omit to read")
		grace = fs.String("grace-period", "", "server-observed quiet period (default 720h)")
		age = fs.String("max-approval-age", "", "approval TTL (default 1h, maximum 72h)")
		revision = fs.Int64("expected-revision", -1, "current server policy revision; 0 creates it")
	case "server-check":
		candidate = fs.String("candidate-deployment", "", "candidate UUID")
		fail = fs.Bool("fail-on-blocked", false, "exit 1 for server blockers")
	case "authorize":
		baseline = fs.String("baseline-deployment", "", "serving deployment UUID")
		candidate = fs.String("candidate-deployment", "", "candidate deployment UUID")
		revision = fs.Int64("expected-revision", -1, "current server policy revision")
		readiness = fs.String("readiness", "", "reviewed migration readiness JSON")
		mapping = fs.String("mapping", "", "reviewed successor mapping JSON")
		acknowledge = fs.Bool("acknowledge-observed-only", false, "acknowledge that quiet telemetry does not prove absence of clients")
		output = fs.String("out", "", "optional new file for the server approval receipt")
	}
	if parseErr := fs.Parse(args); parseErr != nil {
		return 1
	}
	if !validCLISlug(*app) || rejectUnexpectedFlagArgs(fs) {
		return printErr("Invalid arguments", errors.New("provide --app APP and the documented command flags"))
	}
	if name == "policy" && ((*mode != "" && (*mode != "report" && *mode != "enforce" || *revision < 0)) || *mode == "" && (*revision != -1 || *grace != "" || *age != "")) {
		return printErr("Invalid policy", errors.New("provide --mode report|enforce with --expected-revision to write; omit write flags to read"))
	}
	if candidate != nil && !canonicalRouteHealthID(*candidate) {
		return printErr("Invalid candidate", errors.New("provide a canonical candidate deployment UUID"))
	}
	if name == "authorize" && (!canonicalRouteHealthID(*baseline) || *baseline == *candidate || *revision <= 0 || *readiness == "" || *mapping == "" || !*acknowledge) {
		return printErr("Invalid approval", errors.New("provide exact deployment IDs, --expected-revision, --readiness, --mapping and --acknowledge-observed-only"))
	}
	if output != nil && *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new file"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var result any
	switch name {
	case "policy":
		var p api.RouteRemovalPolicy
		if *mode == "" {
			p, err = client.GetRouteRemovalPolicy(ctx, *app)
		} else {
			p, err = client.SetRouteRemovalPolicy(ctx, *app, api.SetRouteRemovalPolicyRequest{ExpectedRevision: revision, Mode: *mode, GracePeriod: *grace, MaxApprovalAge: *age})
		}
		result = p
	case "server-check":
		var check api.RouteRemovalCheck
		check, err = client.CheckRouteRemoval(ctx, *app, *candidate)
		result = check
		if err == nil {
			if jsonOutput {
				if code := jsonOut(writeJSON(result)); code != 0 {
					return code
				}
			} else {
				renderRouteRemovalServerCheck(osStdout, check)
			}
			if *fail && check.Status == "blocked" {
				return 1
			}
			return 0
		}
	case "authorize":
		evidence, readErr := readRouteRemovalGateEvidence(*readiness, *mapping, "")
		if readErr != nil {
			return printErr("Invalid readiness", readErr)
		}
		b, readErr := readRouteMigrationDeploymentSet(ctx, client, map[string]string{*app: *baseline})
		if readErr != nil {
			return printErr("Could not read baseline", readErr)
		}
		c, readErr := readRouteMigrationDeploymentSet(ctx, client, map[string]string{*app: *candidate})
		if readErr != nil {
			return printErr("Could not read candidate", readErr)
		}
		from, to := b[*app], c[*app]
		if from.deployment.AppID != to.deployment.AppID || from.deployment.Status != statusLive || from.deployment.TrafficPercent != 100 || from.captureSHA == "" || to.captureSHA == "" {
			return printErr("Invalid capture", errors.New("use one production app, a serving baseline and authoritative captured SHA-256 metadata"))
		}
		report := buildRouteRemovalGate(*app, *baseline, *candidate, from.evidence.ContractSHA, to.evidence.ContractSHA, "enforce", from.spec, to.spec, evidence, 72*time.Hour, time.Now().UTC())
		refreshRouteRemovalTraffic(ctx, client, &report, 72*time.Hour, time.Now().UTC())
		if len(report.Routes) == 0 || len(report.Blockers) != 1 || report.Blockers[0] != "owner_approval_missing" {
			renderRouteRemovalGate(osStderr, report)
			return printErr("Cannot authorize removal", errors.New("resolve local contract and readiness blockers first"))
		}
		for _, row := range report.Routes {
			if len(row.Blockers) > 0 {
				renderRouteRemovalGate(osStderr, report)
				return printErr("Cannot authorize removal", errors.New("resolve route blockers first"))
			}
		}
		mappings := []api.RouteRemovalMapping{}
		for _, route := range report.Routes {
			m := evidence.mappings[previewCustomerMigrationRouteKey{app: route.From.App, method: route.From.Method, path: route.From.Path}]
			if m.From.App != *app || len(m.Successors) != 1 || m.Successors[0].App != *app {
				return printErr("Invalid server mapping", errors.New("server approvals require exactly one same-app successor per removed operation"))
			}
			to := m.Successors[0]
			mappings = append(mappings, api.RouteRemovalMapping{Method: m.From.Method, Path: m.From.Path, SuccessorMethod: to.Method, SuccessorPath: to.Path})
		}
		sort.Slice(mappings, func(i, j int) bool { return mappings[i].Method+mappings[i].Path < mappings[j].Method+mappings[j].Path })
		var approval api.RouteRemovalApproval
		approval, err = client.ApproveRouteRemoval(ctx, *app, api.ApproveRouteRemovalRequest{ExpectedPolicyRevision: revision, BaselineDeploymentID: *baseline, CandidateDeploymentID: *candidate, BaselineContractSHA256: from.captureSHA, CandidateContractSHA256: to.captureSHA, Mappings: mappings, AcknowledgeObservedOnly: *acknowledge})
		result = approval
		if err == nil && *output != "" {
			body, encodeErr := json.MarshalIndent(approval, "", "  ")
			if encodeErr != nil {
				return printErr("Could not encode approval", encodeErr)
			}
			if writeErr := writeRoutePolicyPlan(*output, append(body, '\n')); writeErr != nil {
				return printErr("Approval stored; receipt file could not be saved", writeErr)
			}
		}
	}
	if err != nil {
		return printErr("Server route removal request failed", err)
	}
	return jsonOut(writeJSON(result))
}

func renderRouteRemovalServerCheck(w io.Writer, check api.RouteRemovalCheck) {
	fmt.Fprintf(w, "Route removal: %s (policy %s, revision %d)\n", check.Status, check.Policy.Mode, check.Policy.Revision)
	if check.EarliestApprovalAt != nil {
		fmt.Fprintf(w, "Earliest approval: %s\n", check.EarliestApprovalAt.UTC().Format(time.RFC3339))
	}
	if check.ApprovalValidUntil != nil {
		fmt.Fprintf(w, "Approval expires: %s\n", check.ApprovalValidUntil.UTC().Format(time.RFC3339))
	}
	for _, blocker := range check.Blockers {
		fmt.Fprintf(w, "Blocker: %s\n", blocker)
	}
	for _, action := range check.NextActions {
		fmt.Fprintf(w, "Next: %s\n", action)
	}
}
