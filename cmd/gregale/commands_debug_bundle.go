package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const debugIncidentBundleSchema = "gregale.debug.bundle/v1"

// debugIncidentBundle is deliberately composed only from debugger-safe API
// DTOs. It is suitable for attaching to a support ticket: request bodies,
// credentials, raw span attributes, and raw SQL never enter the bundle.
type debugIncidentBundle struct {
	SchemaVersion string                           `json:"schema_version"`
	GeneratedAt   string                           `json:"generated_at"`
	AppSlug       string                           `json:"app_slug"`
	Request       api.DebugTelemetryRequestItem    `json:"request"`
	Evidence      api.DebugRequestEvidenceResponse `json:"evidence"`
	Regressions   []api.DebugRegressionItem        `json:"regressions,omitempty"`
	Compare       *api.DebugCompareResponse        `json:"compare,omitempty"`
	Redaction     debugIncidentBundleRedaction     `json:"redaction"`
}

type debugIncidentBundleRedaction struct {
	Profile  string   `json:"profile"`
	Excluded []string `json:"excluded"`
}

// cmdDebugBundle exports the complete read-only investigation context for a
// retained request. The optional deployment pair adds a per-route comparison;
// without it the bundle still contains request evidence and regressions.
func cmdDebugBundle(args []string) int {
	fs := flag.NewFlagSet("debug bundle", flag.ContinueOnError)
	since := fs.String("since", "", "regression/compare lookback window (e.g. 1h, 24h, 3d)")
	route := fs.String("route", "", "route filter for an optional deployment comparison")
	source := fs.String("source", "", "source deployment id for an optional comparison")
	mirror := fs.String("mirror", "", "mirror deployment id for an optional comparison")
	output := fs.String("output", "", "write the redacted JSON bundle to PATH (default stdout; use - for stdout)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"since": true, "route": true, "source": true, "mirror": true, "output": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 2 {
		PrintUsage(os.Stderr, "usage: gregale debug bundle [--since D] [--source ID --mirror ID] [--route P] [--output PATH] <slug> <req_id>", debugCmdDocsTopic)
		return 1
	}
	if (*source == "") != (*mirror == "") {
		return printErr("Incomplete deployment comparison", fmt.Errorf("--source and --mirror must be provided together"))
	}
	if *source != "" && *source == *mirror {
		return printErr("Invalid deployment comparison", fmt.Errorf("--source and --mirror must be different deployments"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	slug, requestID := positional[0], positional[1]
	evidence, err := client.GetAppDebugRequestEvidence(ctx, slug, requestID)
	if err != nil {
		return printErr("Could not get debug request evidence", err)
	}
	regressions, err := client.ListAppDebugRegressions(ctx, slug, *since)
	if err != nil {
		return printErr("Could not list debugger regressions", err)
	}
	bundle := debugIncidentBundle{
		SchemaVersion: debugIncidentBundleSchema,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		AppSlug:       slug,
		Request:       evidence.Request,
		Evidence:      evidence,
		Regressions:   regressions.Regressions,
		Redaction: debugIncidentBundleRedaction{
			Profile:  "debugger-safe",
			Excluded: []string{"request_body", "credentials", "raw_span_attributes", "raw_sql"},
		},
	}
	if *source != "" {
		compare, err := client.CompareAppDebugDeployments(ctx, slug, *source, *mirror, *route, *since, "")
		if err != nil {
			return printErr("Could not compare debugger deployments", err)
		}
		bundle.Compare = &compare
	}
	if err := writeDebugIncidentBundle(*output, bundle); err != nil {
		return printErr("Could not write debugger bundle", err)
	}
	return 0
}

func writeDebugIncidentBundle(output string, bundle debugIncidentBundle) error {
	if output == "" || output == "-" {
		return writeJSONTo(osStdout, bundle)
	}
	b, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if err := writeDebugIncidentBundleFile(output, append(b, '\n')); err != nil {
		return err
	}
	if jsonOutput {
		return writeJSONTo(osStdout, map[string]any{
			"path":           output,
			"schema_version": bundle.SchemaVersion,
			"request_id":     bundle.Request.ID,
		})
	}
	PrintOK(osStdout, "Wrote redacted debugger bundle for request %s to %s.", bundle.Request.ID, output)
	return nil
}

// writeDebugIncidentBundleFile keeps the bundle owner-readable even when a
// caller reuses an existing path whose previous mode was broader than 0600.
func writeDebugIncidentBundleFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
