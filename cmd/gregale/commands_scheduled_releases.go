package main

// ADR-20261021152201: scheduled releases.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdScheduledReleases(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale scheduled-releases <list|show|set|cancel>", "scheduled-releases")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return scheduledReleasesList(args[1:])
	case "show":
		return scheduledReleasesShow(args[1:])
	case "set":
		return scheduledReleasesSet(args[1:])
	case "cancel":
		return scheduledReleasesCancel(args[1:])
	}
	fmt.Fprintf(os.Stderr, "gregale scheduled-releases: unknown subcommand %q\n", args[0])
	return 1
}

// parseReleaseAtFlag turns a --at value into a UTC time: "now", an offset
// such as "+2h30m", an RFC 3339 time, a local "2026-10-22 02:00", or a local
// "02:00" (the next time the clock shows it).
func parseReleaseAtFlag(value string, now time.Time) (time.Time, error) {
	v := strings.TrimSpace(value)
	switch {
	case v == "":
		return time.Time{}, errors.New("--at needs a time")
	case strings.EqualFold(v, "now"):
		return now.UTC(), nil
	case strings.HasPrefix(v, "+"):
		d, err := time.ParseDuration(v[1:])
		if err != nil || d <= 0 {
			return time.Time{}, fmt.Errorf("--at %q: an offset is a positive duration such as +2h30m", v)
		}
		return now.Add(d).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	local := now.Location()
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, v, local); err == nil {
			return t.UTC(), nil
		}
	}
	if clock, err := time.ParseInLocation("15:04", v, local); err == nil {
		t := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, local)
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("--at %q: use now, +2h, 02:00, \"2026-10-22 02:00\" (local time) or an RFC 3339 time", v)
}

// splitChannelList splits a comma-separated --notify value.
func splitChannelList(value string) []string {
	var out []string
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// scheduledRolloutSpec maps --rollout to the canary a schedule starts with;
// empty leaves the choice to the app's release policy.
func scheduledRolloutSpec(rollout string) (*api.CanaryPresetSpec, error) {
	switch rollout {
	case "":
		return nil, nil
	case "safe":
		return &api.CanaryPresetSpec{Preset: api.DefaultReleaseCanaryPreset}, nil
	case "fast":
		return &api.CanaryPresetSpec{Preset: api.FastReleaseCanaryPreset}, nil
	case "immediate":
		return &api.CanaryPresetSpec{Preset: "none"}, nil
	}
	return nil, fmt.Errorf("--rollout must be safe, fast or immediate; got %q", rollout)
}

// scheduledReleaseClient resolves the <deployment> argument, a vN revision
// with --app. Callers check their positionals first.
func scheduledReleaseClient(app, ref string) (*Client, string, int) {
	client, err := authedClient()
	if err != nil {
		return nil, "", printErr("Not logged in", err)
	}
	id, err := resolveDeploymentArg(context.Background(), client, app, ref)
	if err != nil {
		return nil, "", printErr("Could not resolve the release", err)
	}
	return client, id, 0
}

func scheduledReleasesList(args []string) int {
	fs := newFlagSet("scheduled-releases list", flag.ContinueOnError)
	app := fs.String("app", "", "only this app's schedules")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	items, err := client.ListScheduledReleases(context.Background(), *app)
	if err != nil {
		return printErr("Could not list scheduled releases", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(api.ScheduledReleaseListResponse{Items: items}))
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No scheduled releases.")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "APP\tRELEASE\tSTARTS\tSTATE\tROLLOUT\tNOTE")
	for _, sr := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", sr.App, scheduledReleaseRef(sr), localReleaseTime(sr.ReleaseAt),
			sr.State, scheduledRolloutLabel(sr.Canary), orDash(sr.OutcomeDetail))
	}
	_ = tw.Flush()
	return 0
}

func scheduledReleasesShow(args []string) int {
	fs := newFlagSet("scheduled-releases show", flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve a vN revision")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale scheduled-releases show <deployment> [--app <slug>]", "scheduled-releases")
		return 1
	}
	client, id, code := scheduledReleaseClient(*app, fs.Arg(0))
	if client == nil {
		return code
	}
	sr, err := client.GetDeploymentSchedule(context.Background(), id)
	if err != nil {
		return printErr("Could not read the schedule", err)
	}
	return printScheduledRelease(sr, "")
}

func scheduledReleasesSet(args []string) int {
	fs := newFlagSet("scheduled-releases set", flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve a vN revision")
	at := fs.String("at", "", "when the rollout starts: now, +2h, 02:00, \"2026-10-22 02:00\" (local) or RFC 3339")
	rollout := fs.String("rollout", "", "how the release reaches traffic: safe, fast or immediate (default: the app release policy)")
	notify := fs.String("notify", "", "Slack or email channels told when the release starts or cannot start")
	overrideFreeze := fs.String("override-freeze", "", "release into deploy freezes active at that time; the reason is audited")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale scheduled-releases set <deployment> --at <when> [--app <slug>] [--rollout safe|fast|immediate] [--notify CHANNEL,...]", "scheduled-releases")
		return 1
	}
	client, id, code := scheduledReleaseClient(*app, fs.Arg(0))
	if client == nil {
		return code
	}
	when, err := parseReleaseAtFlag(*at, time.Now())
	if err != nil {
		return printErr("Invalid --at", err)
	}
	spec, err := scheduledRolloutSpec(*rollout)
	if err != nil {
		return printErr("Invalid --rollout", err)
	}
	sr, err := client.ScheduleDeployment(context.Background(), id, api.ScheduleReleaseRequest{
		ReleaseAt: when.Format(time.RFC3339), Canary: spec, NotifyChannels: splitChannelList(*notify), OverrideFreeze: *overrideFreeze,
	})
	if err != nil {
		return printErr("Could not schedule the release", err)
	}
	return printScheduledRelease(sr, "Scheduled")
}

func scheduledReleasesCancel(args []string) int {
	fs := newFlagSet("scheduled-releases cancel", flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve a vN revision")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale scheduled-releases cancel <deployment> [--app <slug>]", "scheduled-releases")
		return 1
	}
	client, id, code := scheduledReleaseClient(*app, fs.Arg(0))
	if client == nil {
		return code
	}
	sr, err := client.CancelDeploymentSchedule(context.Background(), id)
	if err != nil {
		return printErr("Could not cancel the schedule", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(sr))
	}
	PrintOK(osStdout, "Cancelled the schedule of %s. It stays built without traffic.", scheduledReleaseRef(sr))
	PrintProgress(osStdout, "Start it later with: gregale scheduled-releases set %s --app %s --at <when>", scheduledReleaseRef(sr), sr.App)
	return 0
}

// printScheduledRelease prints one schedule; verb leads the first line.
func printScheduledRelease(sr api.ScheduledRelease, verb string) int {
	if jsonOutput {
		return jsonOut(writeJSON(sr))
	}
	for i, line := range scheduledReleaseLines(sr, time.Now()) {
		if i == 0 && verb != "" {
			PrintOK(osStdout, "%s %s", verb, line)
			continue
		}
		_, _ = fmt.Fprintln(osStdout, line)
	}
	return 0
}

// scheduledReleaseLines describes a schedule for a terminal.
func scheduledReleaseLines(sr api.ScheduledRelease, now time.Time) []string {
	ref := scheduledReleaseRef(sr)
	lines := []string{fmt.Sprintf("%s of %s: starts %s", ref, sr.App, describeReleaseTime(sr.ReleaseAt, now))}
	switch sr.State {
	case api.ScheduledReleaseStatePending:
		lines = append(lines, "Rollout: "+scheduledRolloutLabel(sr.Canary)+". It waits built, without traffic, until then.")
	case api.ScheduledReleaseStateStarted:
		lines = append(lines, "Started at "+localReleaseTime(sr.StartedAt)+" ("+scheduledRolloutLabel(sr.Canary)+").")
	default:
		lines = append(lines, fmt.Sprintf("State: %s (%s)", sr.State, orDash(sr.OutcomeDetail)))
	}
	if len(sr.NotifyChannels) > 0 {
		lines = append(lines, "Notifies: "+strings.Join(sr.NotifyChannels, ", "))
	}
	if sr.OverrideFreeze != "" {
		lines = append(lines, fmt.Sprintf("Overrides %d deploy freeze(s): %s", len(sr.OverriddenFreezes), sr.OverrideFreeze))
	}
	if sr.State == api.ScheduledReleaseStatePending {
		lines = append(lines, fmt.Sprintf("Move: gregale scheduled-releases set %s --app %s --at <when>", ref, sr.App),
			fmt.Sprintf("Cancel: gregale scheduled-releases cancel %s --app %s", ref, sr.App))
	}
	return lines
}

// scheduledReleaseRef names a scheduled release as vN, or by ID.
func scheduledReleaseRef(sr api.ScheduledRelease) string {
	if sr.Revision > 0 {
		return fmt.Sprintf("v%d", sr.Revision)
	}
	return sr.DeploymentID
}

func scheduledRolloutLabel(spec *api.CanaryPresetSpec) string {
	switch {
	case spec == nil || spec.Preset == "" || spec.Preset == "none":
		return "all traffic at once"
	case spec.Preset == api.DefaultReleaseCanaryPreset:
		return "safe canary"
	case spec.Preset == api.FastReleaseCanaryPreset:
		return "fast canary"
	}
	return spec.Preset + " canary"
}

// localReleaseTime shows an RFC 3339 time in the local zone.
func localReleaseTime(value string) string {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return orDash(value)
	}
	return t.Local().Format("2006-01-02 15:04 MST")
}

// describeReleaseTime is a local time with how far away it is.
func describeReleaseTime(value string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return orDash(value)
	}
	local := t.Local().Format("2006-01-02 15:04 MST")
	d := t.Sub(now).Round(time.Minute)
	switch {
	case d > 0:
		return fmt.Sprintf("%s (in %s)", local, shortDuration(d))
	case d < 0:
		return fmt.Sprintf("%s (%s ago)", local, shortDuration(-d))
	}
	return local + " (now)"
}

// shortDuration renders whole hours and minutes, such as 4h, 45m or 2h30m.
func shortDuration(d time.Duration) string {
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// scheduleTrafficPromotion is `gregale traffic promote --at`: the release
// starts its rollout at that time, through the same checks as a deploy.
func scheduleTrafficPromotion(ctx context.Context, client *Client, deploymentID, at, notify string, guarded bool) int {
	if at == "" {
		return printErr("Invalid flags", errors.New("--release-notify needs --at"))
	}
	if guarded {
		return printErr("Invalid flags", errors.New("--at schedules the promotion; the start runs the server's traffic checks, so drop --if-serving, --require-bindings and the --route-* flags"))
	}
	when, err := parseReleaseAtFlag(at, time.Now())
	if err != nil {
		return printErr("Invalid --at", err)
	}
	sr, err := client.ScheduleDeployment(ctx, deploymentID, api.ScheduleReleaseRequest{
		ReleaseAt: when.Format(time.RFC3339), NotifyChannels: splitChannelList(notify),
	})
	if err != nil {
		return printErr("Could not schedule the promotion", err)
	}
	return printScheduledRelease(sr, "Scheduled")
}

// resolveDeployReleaseAt validates `gregale deploy --at`. It returns the
// release time as RFC 3339, or "" for an immediate release, and the channels
// to notify. unsupported is set for deploys that are not one app's
// production release.
func resolveDeployReleaseAt(at, notify string, explicit map[string]bool, noTraffic, unsupported bool) (string, []string, error) {
	if strings.TrimSpace(at) == "" {
		if strings.TrimSpace(notify) != "" {
			return "", nil, errors.New("--release-notify needs --at")
		}
		return "", nil, nil
	}
	if noTraffic || explicit["traffic-percent"] {
		return "", nil, errors.New("--at keeps the release without traffic until its time; drop --no-traffic and --traffic-percent")
	}
	if unsupported {
		return "", nil, errors.New("--at schedules one app's production release; it cannot be combined with project deploys, --github, previews, --dry-run or --create-only")
	}
	when, err := parseReleaseAtFlag(at, time.Now())
	if err != nil {
		return "", nil, err
	}
	return when.Format(time.RFC3339), splitChannelList(notify), nil
}

// renderPendingSchedule prints the schedule of a release that was just
// built to wait for its time. It reports whether there was one.
func renderPendingSchedule(dep api.DeploymentResponse, appURL string) bool {
	sr := dep.ScheduledRelease
	if sr == nil || sr.State != api.ScheduledReleaseStatePending {
		return false
	}
	ref := scheduledReleaseRef(*sr)
	PrintOK(osStdout, "Scheduled %s: its rollout starts %s.", ref, describeReleaseTime(sr.ReleaseAt, time.Now()))
	PrintProgress(osStdout, "Rollout: %s. Until then it waits built, without traffic; production is unchanged. %s", scheduledRolloutLabel(sr.Canary), appURL)
	PrintProgress(osStdout, "Move: gregale scheduled-releases set %s --app %s --at <when> · Cancel: gregale scheduled-releases cancel %s --app %s", ref, sr.App, ref, sr.App)
	return true
}
