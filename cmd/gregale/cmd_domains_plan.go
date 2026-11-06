package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/domainplan"
	"gopkg.in/yaml.v3"
)

func readDomainArtifact(path string, out any, asJSON bool) error {
	f, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, api.DomainPlanMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > api.DomainPlanMaxBytes {
		return errors.New("domain file exceeds the size limit")
	}
	var decode func(any) error
	if asJSON {
		d := json.NewDecoder(bytes.NewReader(body))
		d.DisallowUnknownFields()
		decode = d.Decode
	} else {
		d := yaml.NewDecoder(bytes.NewReader(body))
		d.KnownFields(true)
		decode = d.Decode
	}
	if err := decode(out); err != nil {
		return err
	}
	var extra any
	if err := decode(&extra); err != io.EOF {
		return errors.New("domain file must contain exactly one document")
	}
	return nil
}

func cmdDomainsPlan(args []string) int {
	fs := newFlagSet("domains plan", flag.ContinueOnError)
	file := fs.String("file", "", "domain manifest YAML or JSON")
	out := fs.String("out", "", "save a new reviewed plan JSON file")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *file == "" {
		return printErr("Invalid domain plan", errors.New("usage: gregale domain plan --file domains.yaml [--out domains.plan.json]"))
	}
	var manifest api.DomainManifest
	if err := readDomainArtifact(*file, &manifest, false); err != nil {
		return printErr("Invalid domain manifest", err)
	}
	manifest, err := domainplan.Normalize(manifest)
	if err != nil {
		return printErr("Invalid domain manifest", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plan, err := client.PlanDomains(ctx, manifest)
	if err != nil {
		return printErr("Could not plan domains", err)
	}
	if err := validateDomainArtifact(plan); err != nil {
		return printErr("Invalid server plan", err)
	}
	if !reflect.DeepEqual(plan.Manifest, manifest) {
		return printErr("Invalid server plan", errors.New("returned manifest differs from the requested bindings"))
	}
	if *out != "" {
		body, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return printErr("Could not encode plan", err)
		}
		if err := writeRoutePolicyPlan(*out, append(body, '\n')); err != nil {
			return printErr("Could not save plan", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(plan))
	}
	renderDomainPlan(plan)
	if *out != "" {
		_, _ = fmt.Fprintf(osStdout, "Saved plan: %s\n", previewReportText(*out))
	}
	return 0
}

func validateDomainArtifact(p api.DomainPlan) error {
	m, err := domainplan.Normalize(p.Manifest)
	if err != nil || p.Version != 1 || !domainplan.ValidDigest(p.SHA256) || !reflect.DeepEqual(m, p.Manifest) || len(p.Changes) != len(m.Domains) {
		return errors.New("invalid domain plan identity or manifest")
	}
	for i, c := range p.Changes {
		s := m.Domains[i]
		if c.Domain != s.Domain || c.After.App != s.App || c.After.AppID == "" || c.After.Environment != s.Environment || (c.Action != "create" && c.Action != "move" && c.Action != "noop") || (c.Action == "create") != (c.Before == nil) {
			return errors.New("domain plan changes do not match its manifest")
		}
	}
	return nil
}

func renderDomainPlan(p api.DomainPlan) {
	_, _ = fmt.Fprintf(osStdout, "Domain plan %s\n", p.SHA256)
	for _, c := range p.Changes {
		before := "unattached"
		if c.Before != nil {
			before = domainTargetLabel(*c.Before)
		}
		_, _ = fmt.Fprintf(osStdout, "  %s %s: %s -> %s\n", c.Action, previewReportText(c.Domain), previewReportText(before), previewReportText(domainTargetLabel(c.After)))
		if c.ClearsDefault {
			_, _ = fmt.Fprintln(osStdout, "    Clears the source app's default domain; its URL falls back to the platform hostname.")
		}
	}
	_, _ = fmt.Fprintln(osStdout, "Only these bindings change. New claims need DNS verification. Moves retain proof and certificate state; application behavior still needs a cutover check.")
}

func domainTargetLabel(t api.DomainBindingTarget) string {
	if t.Environment != "" {
		return t.App + " (" + t.Environment + ")"
	}
	return t.App + " (default routing)"
}

func cmdDomainsApply(args []string) int {
	fs := newFlagSet("domains apply", flag.ContinueOnError)
	file := fs.String("plan", "", "reviewed domain plan JSON")
	confirm := fs.Bool("confirm", false, "apply all reviewed changes")
	rollback := fs.String("rollback-out", "", "save the previous bindings as a new manifest")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *file == "" || !*confirm {
		return printErr("Invalid domain apply", errors.New("usage: gregale domain apply --plan domains.plan.json --confirm [--rollback-out rollback.json]"))
	}
	var plan api.DomainPlan
	if err := readDomainArtifact(*file, &plan, true); err != nil {
		return printErr("Invalid domain plan", err)
	}
	if err := validateDomainArtifact(plan); err != nil {
		return printErr("Invalid domain plan", err)
	}
	return applyReviewedDomains(plan, *rollback)
}

func applyReviewedDomains(plan api.DomainPlan, rollback string) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Compare the complete public diff too: editing a saved artifact must not
	// let its displayed before/after disagree with the server's reviewed action.
	current, err := client.PlanDomains(ctx, plan.Manifest)
	if err != nil {
		return printErr("Could not refresh domain plan", err)
	}
	if !reflect.DeepEqual(current, plan) {
		return printErr("Domain plan changed", errors.New("generate and review a new plan before applying"))
	}
	// Persist recovery instructions before committing. A bad or existing output
	// path must never leave a successful move without the requested artifact.
	if previous := domainplan.Rollback(plan); rollback != "" && previous != nil {
		body, err := json.MarshalIndent(previous, "", "  ")
		if err == nil {
			err = writeRoutePolicyPlan(rollback, append(body, '\n'))
		}
		if err != nil {
			return printErr("Could not save rollback manifest; bindings have not changed", err)
		}
	}
	response, err := client.ApplyDomains(ctx, api.DomainApplyRequest{Manifest: plan.Manifest, ExpectedPlanSHA256: plan.SHA256, Confirm: true})
	if err != nil {
		return printErr("Could not apply domains; inspect current bindings before preparing another plan", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	_, _ = fmt.Fprintln(osStdout, "Domain bindings committed.")
	for _, d := range response.Domains {
		if !d.Verified {
			printDomainDNSRecords(osStdout, d)
		}
	}
	if response.Rollback != nil {
		body, _ := json.MarshalIndent(response.Rollback, "", "  ")
		_, _ = fmt.Fprintf(osStdout, "Previous bindings (plan and apply this manifest to move back):\n%s\n", body)
	}
	return 0
}

func cmdDomainsMove(args []string) int {
	fs := newFlagSet("domains move", flag.ContinueOnError)
	app := fs.String("app", "", "destination app slug")
	environment := fs.String("environment", "", "destination project environment; omitted selects default routing")
	confirm := fs.Bool("confirm", false, "confirm moving the verified binding")
	dry := fs.Bool("dry-run", false, "show the move without applying")
	rollback := fs.String("rollback-out", "", "save the previous binding as a new manifest")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 1 || *app == "" || (!*confirm && !*dry) || (*confirm && *dry) {
		return printErr("Invalid domain move", errors.New("usage: gregale domain move <domain> --app SLUG [--environment ENV] --dry-run | --confirm [--rollback-out PATH]"))
	}
	m, err := domainplan.Normalize(api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: fs.Arg(0), App: *app, Environment: *environment}}})
	if err != nil {
		return printErr("Invalid domain move", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := client.PlanDomains(ctx, m)
	if err != nil {
		return printErr("Could not plan domain move", err)
	}
	if err := validateDomainArtifact(p); err != nil {
		return printErr("Invalid server plan", err)
	}
	if !reflect.DeepEqual(p.Manifest, m) {
		return printErr("Invalid server plan", errors.New("returned manifest differs from the requested move"))
	}
	if p.Changes[0].Action == "create" {
		return printErr("Domain is unattached", errors.New("use domain create or a declarative plan to create a claim"))
	}
	if !jsonOutput {
		renderDomainPlan(p)
	}
	if *dry {
		if jsonOutput {
			return jsonOut(writeJSON(p))
		}
		return 0
	}
	return applyReviewedDomains(p, *rollback)
}
