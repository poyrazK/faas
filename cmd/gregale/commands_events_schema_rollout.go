package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsSchemaRollout(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events schema-rollout-preview", flag.ContinueOnError)
	version := fs.String("version", "", "proposed or registered schema version")
	schema := fs.String("schema", "", "proposed JSON Schema; JSON, @file or -; omitted uses registered version")
	samples := fs.String("samples", "", "JSON array of sample event data values; JSON, @file or -")
	from := fs.String("from", "", "retained acceptance range start (RFC3339)")
	until := fs.String("until", "", "retained acceptance range end (RFC3339, exclusive)")
	limit := fs.Int("retained-limit", 0, "maximum matching retained payloads to check (1..100; default 100)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events schema-rollout-preview SOURCE TYPE --version v2 [--schema @schema.json] [--samples @samples.json] [--from RFC3339 --until RFC3339]", "events")
		return 1
	}
	if *schema == "-" && *samples == "-" {
		return printErr("Invalid input", fmt.Errorf("only one input may read stdin"))
	}
	req := api.EventSchemaRolloutRequest{Source: positional[0], Type: positional[1], Version: *version, RetainedLimit: *limit}
	if *schema != "" {
		value, err := resolveJSONFlag("--schema", *schema)
		if err != nil {
			return printErr("Invalid schema", err)
		}
		req.Schema = json.RawMessage(value)
	}
	if *samples != "" {
		value, err := resolveJSONFlag("--samples", *samples)
		if err != nil {
			return printErr("Invalid samples", err)
		}
		if !bytes.HasPrefix(bytes.TrimSpace(value), []byte("[")) {
			return printErr("Invalid samples", fmt.Errorf("provide a JSON array of event data values"))
		}
		if err = json.Unmarshal(value, &req.Samples); err != nil {
			return printErr("Invalid samples", fmt.Errorf("provide a JSON array of sample event data values"))
		}
	}
	for _, input := range []struct {
		value  string
		target **time.Time
	}{{*from, &req.From}, {*until, &req.Until}} {
		if input.value != "" {
			parsed, err := time.Parse(time.RFC3339, input.value)
			if err != nil {
				return printErr("Invalid range", err)
			}
			*input.target = &parsed
		}
	}
	if err := req.Validate(); err != nil {
		return printErr("Invalid rollout preview", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.PreviewEventSchemaRollout(context.Background(), req)
	if err != nil {
		return printErr("Schema rollout preview failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	writeEventSchemaRollout(out)
	return 0
}
func writeEventSchemaRollout(r api.EventSchemaRolloutResponse) {
	_, _ = fmt.Fprintf(osStdout, "Schema %s / %s / %s (%s)\n", oneLine(r.Source), oneLine(r.Type), oneLine(r.Version), r.SchemaOrigin)
	_, _ = fmt.Fprintf(osStdout, "Consumers: %d | accept version %d | exclude version %d | truncated %t\n", r.ConsumerCount, r.AcceptingCount, r.ExcludingCount, r.ConsumersTruncated)
	_, _ = fmt.Fprintln(osStdout, "Version acceptance does not evaluate content filters or guarantee delivery.")
	for _, c := range r.Consumers {
		_, _ = fmt.Fprintf(osStdout, "%s | app %s | accepts %t | versions %s | content filter %t\n", oneLine(c.SubscriptionID), oneLine(c.AppID), c.AcceptsVersion, oneLine(eventSchemaVersionsLabel(c.SchemaVersions)), c.ContentFilterPresent)
	}
	_, _ = fmt.Fprintf(osStdout, "Samples: valid %d | invalid %d\n", r.SampleValidCount, r.SampleInvalidCount)
	for _, v := range r.Samples {
		if !v.Valid {
			_, _ = fmt.Fprintf(osStdout, "sample %d | %s | field %s\n", *v.SampleIndex, oneLine(v.Reason), oneLine(v.Field))
		}
	}
	if r.Retained.Requested {
		_, _ = fmt.Fprintf(osStdout, "Retained: scanned %d account receipts | examined %d matching payloads | valid %d | invalid %d | unreadable %d | truncated %t\n", r.Retained.ScannedCount, r.Retained.ExaminedCount, r.Retained.ValidCount, r.Retained.InvalidCount, r.Retained.UnreadableCount, r.Retained.Truncated)
		for _, v := range r.Retained.Results {
			if !v.Valid {
				_, _ = fmt.Fprintf(osStdout, "event %s | %s | field %s\n", oneLine(v.EventID), oneLine(v.Reason), oneLine(v.Field))
			}
		}
		_, _ = fmt.Fprintln(osStdout, "Recent retained samples are bounded; history is incomplete. This is not a schema compatibility proof.")
	}
}
