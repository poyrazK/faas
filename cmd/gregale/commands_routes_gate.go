package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRoutesGate(args []string) int {
	if len(args) == 0 || args[0] != "get" && args[0] != "set" {
		return printErr("Invalid route gate command", errors.New("usage: gregale routes gate <get|set> <slug> [--mode report|enforce --expected-revision N]"))
	}
	action := args[0]
	flags, positional := splitArgsForFlags(args[1:])
	fs := newFlagSet("routes gate "+action, flag.ContinueOnError)
	var mode string
	var revision int64
	if action == "set" {
		fs.StringVar(&mode, "mode", "", "report or enforce lifecycle traffic checks and canary route requirements")
		fs.Int64Var(&revision, "expected-revision", -1, "current gate revision; use 0 initially")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || action == "set" && (mode != "report" && mode != "enforce" || revision < 0 || revision > api.RouteRequirementsMaxRevision) {
		return printErr("Invalid route gate command", errors.New("supply an app slug and, for set, --mode report|enforce and --expected-revision N"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	var gate api.CanaryRouteGate
	if action == "set" {
		gate, err = client.SetCanaryRouteGate(ctx, positional[0], api.SetCanaryRouteGateRequest{Mode: mode, ExpectedRevision: &revision})
	} else {
		gate, err = client.GetCanaryRouteGate(ctx, positional[0])
	}
	if err != nil {
		return printErr("Could not read or change the canary route gate", err)
	}
	if err := validateCanaryRouteGateResult(gate); err != nil {
		return printErr("Invalid route gate response", err)
	}
	if action == "set" && (gate.Mode != mode || gate.Revision != revision && gate.Revision != revision+1) {
		return printErr("Invalid route gate response", errors.New("mode or revision does not match the submitted update"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(gate))
	}
	_, _ = fmt.Fprintf(osStdout, "Canary route gate for %s\nMode: %s\nRevision: %d\n", positional[0], gate.Mode, gate.Revision)
	return 0
}

func validateCanaryRouteGateResult(gate api.CanaryRouteGate) error {
	_, err := uuid.Parse(gate.AppID)
	if err != nil || gate.Mode != "report" && gate.Mode != "enforce" || gate.Revision < 0 || gate.Revision > api.RouteRequirementsMaxRevision || gate.Revision == 0 && gate.Mode != "report" || gate.Revision > 0 && gate.UpdatedAt == nil {
		return errors.New("route gate identity, mode or revision is invalid")
	}
	return nil
}
