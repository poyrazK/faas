package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// profileCapturePollInterval paces status polls while a capture runs.
var profileCapturePollInterval = time.Second

const profileCaptureUsage = "usage: gregale debug capture <slug> [--type cpu,heap,stacks] [--duration 10s] [--instance UUID] [--output DIR] [--top N] [--no-wait]"

// cmdDebugCapture runs an on-demand CPU/heap capture on a running instance
// (ADR-1155), waits for it and prints the hottest functions per kind.
func cmdDebugCapture(args []string) int {
	fs := newFlagSet("debug capture", flag.ContinueOnError)
	kinds := fs.String("type", "cpu", "profile kinds, comma-separated: cpu, heap, stacks, block, mutex")
	duration := fs.Duration("duration", api.ProfileCaptureDefaultDuration, "capture window (1s..60s)")
	instance := fs.String("instance", "", "instance UUID; default: the app's earliest running instance")
	output := fs.String("output", "", "directory for the merged .pb.gz pprof files")
	top := fs.Int("top", 15, "functions to print per kind")
	noWait := fs.Bool("no-wait", false, "queue the capture and print its id")
	flags, positional := normalizeDebugFlagArgs(args, map[string]bool{"type": true, "duration": true, "instance": true, "output": true, "top": true})
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(osStderr, profileCaptureUsage, debugCmdDocsTopic)
		return 1
	}
	req := api.CreateProfileCaptureRequest{Kinds: strings.Split(*kinds, ","), DurationSeconds: int(duration.Round(time.Second) / time.Second), InstanceID: *instance}
	if err := req.Normalize(); err != nil {
		return printErr("Invalid capture", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	capture, err := client.CreateProfileCapture(ctx, positional[0], req)
	if err != nil {
		return printErr("Could not start profile capture", err)
	}
	if *noWait {
		return printProfileCapture(capture)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Capturing %s for %ds (capture %s)…\n", strings.Join(req.Kinds, "+"), req.DurationSeconds, capture.ID)
	}
	capture, err = waitProfileCapture(ctx, client, positional[0], capture)
	if err != nil {
		return printErr("Profile capture did not finish", err)
	}
	return showProfileCapture(ctx, client, positional[0], capture, *output, *top)
}

const profileCapturesUsage = "usage: gregale debug captures <slug> [<capture-id>] [--output DIR] [--top N] [--delete]"

// cmdDebugCaptures lists an app's captures, shows one, or deletes one with
// --delete, freeing its share of the account's capture storage (ADR-1155).
func cmdDebugCaptures(args []string) int {
	fs := newFlagSet("debug captures", flag.ContinueOnError)
	output := fs.String("output", "", "directory for the merged .pb.gz pprof files")
	top := fs.Int("top", 15, "functions to print per kind")
	del := fs.Bool("delete", false, "delete the named capture and its profiles")
	flags, positional := normalizeDebugFlagArgs(args, map[string]bool{"output": true, "top": true})
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) < 1 || len(positional) > 2 || *del && len(positional) != 2 {
		PrintUsage(osStderr, profileCapturesUsage, debugCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *del {
		if err := client.DeleteProfileCapture(ctx, positional[0], positional[1]); err != nil {
			return printErr("Could not delete profile capture", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"app": positional[0], "id": positional[1], "deleted": true}))
		}
		_, _ = fmt.Fprintf(osStdout, "Deleted profile capture %s.\n", positional[1])
		return 0
	}
	if len(positional) == 2 {
		capture, err := client.GetProfileCapture(ctx, positional[0], positional[1])
		if err != nil {
			return printErr("Could not get profile capture", err)
		}
		return showProfileCapture(ctx, client, positional[0], capture, *output, *top)
	}
	list, err := client.ListProfileCaptures(ctx, positional[0])
	if err != nil {
		return printErr("Could not list profile captures", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if _, err := fmt.Fprintln(osStdout, "ID\tSTATUS\tTRIGGER\tKINDS\tDURATION\tINSTANCE\tCREATED"); err != nil {
		return printErr("Could not write captures", err)
	}
	for _, c := range list.Captures {
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%ds\t%s\t%s\n", c.ID, c.Status, profileCaptureTrigger(c), strings.Join(c.Kinds, ","), c.DurationSeconds, c.InstanceID, c.CreatedAt.Format(time.RFC3339)); err != nil {
			return printErr("Could not write captures", err)
		}
	}
	return 0
}

func waitProfileCapture(ctx context.Context, client *Client, slug string, capture api.ProfileCapture) (api.ProfileCapture, error) {
	deadline := time.Now().Add(time.Duration(capture.DurationSeconds)*time.Second + 2*time.Minute)
	for !capture.Done() {
		if time.Now().After(deadline) {
			return capture, fmt.Errorf("capture %s is still %s; check it later with `gregale debug captures %s %s`", capture.ID, capture.Status, slug, capture.ID)
		}
		time.Sleep(profileCapturePollInterval)
		next, err := client.GetProfileCapture(ctx, slug, capture.ID)
		if err != nil {
			return capture, err
		}
		capture = next
	}
	return capture, nil
}

func printProfileCapture(c api.ProfileCapture) int {
	if jsonOutput {
		return jsonOut(writeJSON(c))
	}
	_, err := fmt.Fprintf(osStdout, "Capture %s: %s\n", c.ID, c.Status)
	if err == nil && c.Trigger == api.ProfileCaptureTriggerMemoryPressure {
		_, err = fmt.Fprintf(osStdout, "Captured automatically on memory pressure: %s\n", c.TriggerDetail)
	}
	if err == nil && c.Trigger == api.ProfileCaptureTriggerSlowRequest {
		_, err = fmt.Fprintf(osStdout, "Captured automatically while a request was stuck: %s\n", c.TriggerDetail)
		if err == nil && c.RequestID != "" {
			_, err = fmt.Fprintf(osStdout, "Request: gregale debug requests show <slug> %s\n", c.RequestID)
		}
	}
	if err == nil && c.Trigger == api.ProfileCaptureTriggerCPUSaturation {
		_, err = fmt.Fprintf(osStdout, "Captured automatically on CPU saturation: %s\n", c.TriggerDetail)
	}
	if err == nil && c.InstanceID != "" {
		_, err = fmt.Fprintf(osStdout, "Instance %s, deployment %s, %d instrumented process(es)\n", c.InstanceID, c.DeploymentID, c.Processes)
	}
	if err == nil && c.Reason != "" {
		_, err = fmt.Fprintln(osStdout, c.Reason)
	}
	if err == nil && c.OOMKilledAt != nil {
		_, err = fmt.Fprintf(osStdout, "The instance was OOM-killed at %s (%d MB of a %d MB plan); the heap profile shows memory retained before the kill.\n", c.OOMKilledAt.Format(time.RFC3339), c.OOMPeakMB, c.OOMPlanMB)
	}
	if err != nil {
		return printErr("Could not write capture", err)
	}
	return 0
}

const profileCaptureDiffUsage = "usage: gregale debug capture-diff <slug> <capture-id> --base <capture-id> [--type cpu|heap] [--top N]"

// cmdDebugCaptureDiff compares a capture with an earlier one (ADR-1155).
func cmdDebugCaptureDiff(args []string) int {
	fs := newFlagSet("debug capture-diff", flag.ContinueOnError)
	base := fs.String("base", "", "baseline capture id of the same app")
	kind := fs.String("type", "", "cpu or heap; default a kind both captures collected, preferring heap")
	top := fs.Int("top", 15, "functions to print")
	flags, positional := normalizeDebugFlagArgs(args, map[string]bool{"base": true, "type": true, "top": true})
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || *base == "" {
		PrintUsage(osStderr, profileCaptureDiffUsage, debugCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	diff, err := client.DiffProfileCaptures(context.Background(), positional[0], positional[1], *base, *kind)
	if err != nil {
		return printErr("Could not compare profile captures", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(diff))
	}
	if err := printProfileCaptureDiff(diff, *top); err != nil {
		return printErr("Could not write comparison", err)
	}
	return 0
}

func printProfileCaptureDiff(d api.ProfileCaptureDiff, top int) error {
	if _, err := fmt.Fprintf(osStdout, "%s: %s → %s (%s)\n", strings.ToUpper(d.Kind), profileCaptureAmount(d.BaselineTotal, d.Unit),
		profileCaptureAmount(d.CandidateTotal, d.Unit), profileCaptureDelta(d.Delta, d.Unit)); err != nil {
		return err
	}
	for _, w := range d.Warnings {
		if _, err := fmt.Fprintln(osStdout, "Note: "+w); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(osStdout, "CHANGE\tBASELINE\tCANDIDATE\tFUNCTION\tLOCATION"); err != nil {
		return err
	}
	for i, f := range d.Functions {
		if i >= top {
			break
		}
		location := f.File
		if f.Line > 0 {
			location = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		if f.Source != nil {
			location = f.Source.URL
		}
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\n", profileCaptureDelta(f.Delta, d.Unit), profileCaptureAmount(f.Baseline, d.Unit),
			profileCaptureAmount(f.Candidate, d.Unit), f.Name, location); err != nil {
			return err
		}
	}
	return nil
}

func profileCaptureDelta(v int64, unit string) string {
	if v < 0 {
		return "-" + profileCaptureAmount(-v, unit)
	}
	return "+" + profileCaptureAmount(v, unit)
}

// showProfileCapture prints a finished capture's top functions per kind and
// optionally writes the merged pprof files.
func showProfileCapture(ctx context.Context, client *Client, slug string, c api.ProfileCapture, output string, top int) int {
	if c.Status != api.ProfileCaptureReady || len(c.Profiles) == 0 {
		if code := printProfileCapture(c); code != 0 || c.Status == api.ProfileCaptureReady {
			return code
		}
		return 1
	}
	views := map[string]api.ProfileCaptureView{}
	for _, kind := range c.Kinds {
		view, err := client.GetProfileCaptureView(ctx, slug, c.ID, kind)
		if err != nil {
			return printErr("Could not read "+kind+" profile", err)
		}
		views[kind] = view
		if output != "" {
			if code := saveProfileCapture(ctx, client, slug, c, kind, output); code != 0 {
				return code
			}
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"capture": c, "views": views}))
	}
	if code := printProfileCapture(c); code != 0 {
		return code
	}
	for _, kind := range c.Kinds {
		if err := printProfileCaptureView(views[kind], top); err != nil {
			return printErr("Could not write profile", err)
		}
	}
	return 0
}

func saveProfileCapture(ctx context.Context, client *Client, slug string, c api.ProfileCapture, kind, dir string) int {
	body, err := client.DownloadProfileCapture(ctx, slug, c.ID, kind)
	if err != nil {
		return printErr("Could not download "+kind+" profile", err)
	}
	path := filepath.Join(dir, "profile-"+c.ID+"-"+kind+".pb.gz")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return printErr("Could not write "+kind+" profile", err)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Wrote %s (open with `go tool pprof -http=: %s`)\n", path, path)
	}
	return 0
}

func printProfileCaptureView(v api.ProfileCaptureView, top int) error {
	if _, err := fmt.Fprintf(osStdout, "\n%s: %s total\n", strings.ToUpper(v.Kind), profileCaptureAmount(v.Total, v.Unit)); err != nil {
		return err
	}
	if v.Empty {
		_, err := fmt.Fprintln(osStdout, "No samples were captured.")
		return err
	}
	if _, err := fmt.Fprintln(osStdout, "SELF\tTOTAL\tFUNCTION\tLOCATION"); err != nil {
		return err
	}
	for i, f := range v.Functions {
		if i >= top {
			break
		}
		location := f.File
		if f.Line > 0 {
			location = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		if f.Source != nil {
			location = f.Source.URL
		}
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", profileCaptureAmount(f.Self, v.Unit), profileCaptureAmount(f.Total, v.Unit), f.Name, location); err != nil {
			return err
		}
	}
	return nil
}

func profileCaptureTrigger(c api.ProfileCapture) string {
	if c.Trigger == "" {
		return api.ProfileCaptureTriggerManual
	}
	return c.Trigger
}

func profileCaptureAmount(v int64, unit string) string {
	if unit == "count" {
		if v == 1 {
			return "1 thread"
		}
		return fmt.Sprintf("%d threads", v)
	}
	if unit == "bytes" {
		switch {
		case v >= 1<<30:
			return fmt.Sprintf("%.2f GiB", float64(v)/(1<<30))
		case v >= 1<<20:
			return fmt.Sprintf("%.2f MiB", float64(v)/(1<<20))
		case v >= 1<<10:
			return fmt.Sprintf("%.1f KiB", float64(v)/(1<<10))
		}
		return fmt.Sprintf("%d B", v)
	}
	return time.Duration(v).Round(time.Microsecond).String()
}
