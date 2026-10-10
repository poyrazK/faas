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

const profileCaptureUsage = "usage: gregale debug capture <slug> [--type cpu,heap] [--duration 10s] [--instance UUID] [--output DIR] [--top N] [--no-wait]"

// cmdDebugCapture runs an on-demand CPU/heap capture on a running instance
// (ADR-967), waits for it and prints the hottest functions per kind.
func cmdDebugCapture(args []string) int {
	fs := newFlagSet("debug capture", flag.ContinueOnError)
	kinds := fs.String("type", "cpu", "profile kinds: cpu, heap or cpu,heap")
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

const profileCapturesUsage = "usage: gregale debug captures <slug> [<capture-id>] [--output DIR] [--top N]"

// cmdDebugCaptures lists an app's captures or shows one.
func cmdDebugCaptures(args []string) int {
	fs := newFlagSet("debug captures", flag.ContinueOnError)
	output := fs.String("output", "", "directory for the merged .pb.gz pprof files")
	top := fs.Int("top", 15, "functions to print per kind")
	flags, positional := normalizeDebugFlagArgs(args, map[string]bool{"output": true, "top": true})
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) < 1 || len(positional) > 2 {
		PrintUsage(osStderr, profileCapturesUsage, debugCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
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
	if _, err := fmt.Fprintln(osStdout, "ID\tSTATUS\tKINDS\tDURATION\tINSTANCE\tCREATED"); err != nil {
		return printErr("Could not write captures", err)
	}
	for _, c := range list.Captures {
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%ds\t%s\t%s\n", c.ID, c.Status, strings.Join(c.Kinds, ","), c.DurationSeconds, c.InstanceID, c.CreatedAt.Format(time.RFC3339)); err != nil {
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
	if err == nil && c.InstanceID != "" {
		_, err = fmt.Fprintf(osStdout, "Instance %s, deployment %s, %d instrumented process(es)\n", c.InstanceID, c.DeploymentID, c.Processes)
	}
	if err == nil && c.Reason != "" {
		_, err = fmt.Fprintln(osStdout, c.Reason)
	}
	if err != nil {
		return printErr("Could not write capture", err)
	}
	return 0
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
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", profileCaptureAmount(f.Self, v.Unit), profileCaptureAmount(f.Total, v.Unit), f.Name, location); err != nil {
			return err
		}
	}
	return nil
}

func profileCaptureAmount(v int64, unit string) string {
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
