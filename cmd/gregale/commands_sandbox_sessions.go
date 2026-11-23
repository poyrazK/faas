package main

// `gregale sandbox session` (ADR-20261122132055 step 10): list, read, pause,
// resume, fork and close sandbox sessions. `gregale sandbox` itself is the
// agent runs group, so sessions sit under their own subnoun (owner's call).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	sandboxSessionsUsage      = "usage: gregale sandbox session <list|get|pause|resume|fork|rm> [args]"
	sandboxSessionListUsage   = "usage: gregale sandbox session list [--paused] [--state STATE] [--limit N]"
	sandboxSessionPauseUsage  = "usage: gregale sandbox session pause <id> [--no-wait] [--wait-timeout D]"
	sandboxSessionResumeUsage = "usage: gregale sandbox session resume <id> [--no-wait] [--wait-timeout D]"
	sandboxSessionForkUsage   = "usage: gregale sandbox session fork <id> [--count N] [--kind ephemeral|persistent]"
	sandboxSessionPollDefault = 2 * time.Second
)

// sandboxSessionPoll is how often pause and resume waits poll; tests shorten it.
var sandboxSessionPoll = sandboxSessionPollDefault

func cmdSandboxSessions(args []string) int {
	parent, _ := lookupCliCommand("sandbox-sessions")
	if len(args) == 0 {
		PrintUsage(os.Stderr, sandboxSessionsUsage, "sandbox-sessions")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdSandboxSessionList(args[1:])
	case subGet:
		return cmdSandboxSessionGet(args[1:])
	case "pause":
		return cmdSandboxSessionPauseResume(args[1:], true)
	case "resume":
		return cmdSandboxSessionPauseResume(args[1:], false)
	case "fork":
		return cmdSandboxSessionFork(args[1:])
	case subRm:
		return cmdSandboxSessionRm(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown sandbox session subcommand %q\n", args[0])
	return unknownSubcommandChoices(os.Stderr, "sandbox-sessions", parent, args[0])
}

func cmdSandboxSessionList(args []string) int {
	flags, positional := splitArgsForFlags(args, "paused")
	fs := newFlagSet("sandbox session list", flag.ContinueOnError)
	paused := fs.Bool("paused", false, "only paused persistent sessions")
	state := fs.String("state", "", "only sessions in this state")
	limit := fs.Int("limit", 0, "sessions per page (default 50)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || len(positional) != 0 || (*paused && *state != "" && *state != "paused") {
		PrintUsage(os.Stderr, sandboxSessionListUsage, "sandbox-sessions")
		return 1
	}
	if *paused {
		*state = "paused"
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListSandboxes(context.Background(), *state, "", *limit)
	if err != nil {
		return printErr("Could not list sandbox sessions", err)
	}
	for i := range out.Items {
		out.Items[i].Token, out.Items[i].TokenExpiresAt = "", nil
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out.Items))
	}
	if len(out.Items) == 0 {
		printEmptyResult("sandbox sessions", *state, "")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-36s  %-9s  %-10s  %7s  %-20s  %s\n", "ID", "STATE", "KIND", "RAM_MB", "PAUSED", "STORED")
	for _, s := range out.Items {
		_, _ = fmt.Fprintf(osStdout, "%-36s  %-9s  %-10s  %7d  %-20s  %s\n", s.ID, s.State, s.Kind, s.RAMMB, sandboxSessionTime(s.PausedAt), sandboxSessionBytes(s.StoredBytes))
	}
	if out.NextBefore != "" {
		_, _ = fmt.Fprintln(os.Stderr, "More sessions exist; narrow with --state or --paused.")
	}
	return 0
}

func cmdSandboxSessionGet(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale sandbox session get <id>", "sandbox-sessions")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	s, err := client.GetSandbox(context.Background(), args[0])
	if err != nil {
		return printErr("Could not read the sandbox session", err)
	}
	// The view's data-plane token is a credential; the CLI never prints it.
	s.Token, s.TokenExpiresAt = "", nil
	if jsonOutput {
		return jsonOut(writeJSON(s))
	}
	printSandboxSession(s)
	return 0
}

func printSandboxSession(s api.Sandbox) {
	row := func(k, v string) {
		if v != "" {
			_, _ = fmt.Fprintf(osStdout, "%-12s %s\n", k+":", v)
		}
	}
	row("id", s.ID)
	row("state", s.State)
	row("kind", s.Kind)
	row("source", firstNonEmpty(s.Template, s.Image, s.Preset))
	row("ram_mb", fmt.Sprint(s.RAMMB))
	if s.VCPU > 0 {
		row("vcpu", fmt.Sprint(s.VCPU))
	}
	if s.VolumeGB > 0 {
		row("volume_gb", fmt.Sprint(s.VolumeGB))
	}
	row("created", s.CreatedAt.UTC().Format(time.RFC3339))
	row("expires", s.ExpiresAt.UTC().Format(time.RFC3339))
	if s.PausedAt != nil {
		row("paused", sandboxSessionTime(s.PausedAt))
		row("durability", s.Durability)
	}
	if s.StoredBytes > 0 {
		row("stored", sandboxSessionBytes(s.StoredBytes))
	}
	if s.ResumedCold {
		row("resumed", "cold (memory from before the last pause was lost)")
	}
	row("forked_from", s.ForkedFrom)
	if s.Outcome != nil {
		row("outcome", strings.TrimSpace(s.Outcome.Code+" "+s.Outcome.Message))
	}
}

func cmdSandboxSessionPauseResume(args []string, pause bool) int {
	usage, verb, want := sandboxSessionResumeUsage, "resume", "ready"
	if pause {
		usage, verb, want = sandboxSessionPauseUsage, "pause", "paused"
	}
	flags, positional := splitArgsForFlags(args, "no-wait")
	fs := newFlagSet("sandbox session "+verb, flag.ContinueOnError)
	noWait := fs.Bool("no-wait", false, "ask and return without waiting")
	waitTimeout := fs.Duration("wait-timeout", 5*time.Minute, "maximum time to wait")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || len(positional) != 1 {
		PrintUsage(os.Stderr, usage, "sandbox-sessions")
		return 1
	}
	id := positional[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	call := client.ResumeSandbox
	if pause {
		call = client.PauseSandbox
	}
	s, err := call(ctx, id)
	if err != nil {
		return printErr("Could not "+verb+" the sandbox session", err)
	}
	if !*noWait {
		if s, err = waitSandboxSession(ctx, client, id, want, *waitTimeout); err != nil {
			return printErr("Could not "+verb+" the sandbox session", err)
		}
	}
	s.Token, s.TokenExpiresAt = "", nil
	if jsonOutput {
		return jsonOut(writeJSON(s))
	}
	PrintOK(osStdout, "Sandbox session %s is %s", id, s.State)
	return 0
}

// waitSandboxSession polls until the session reaches want. A session that
// ends on the way is an error, as is one still on its way at timeout.
func waitSandboxSession(ctx context.Context, client *api.Client, id, want string, timeout time.Duration) (api.Sandbox, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		s, err := client.GetSandbox(ctx, id)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return s, fmt.Errorf("still not %s after %s; check it with `gregale sandbox session get %s`", want, timeout, id)
			}
			return s, err
		}
		switch s.State {
		case want:
			return s, nil
		case "closed", "expired", "failed":
			outcome := ""
			if s.Outcome != nil {
				outcome = ": " + strings.TrimSpace(s.Outcome.Code+" "+s.Outcome.Message)
			}
			return s, fmt.Errorf("the session %s%s", s.State, outcome)
		}
		select {
		case <-ctx.Done():
			return s, fmt.Errorf("still %s after %s; check it with `gregale sandbox session get %s`", s.State, timeout, id)
		case <-time.After(sandboxSessionPoll):
		}
	}
}

func cmdSandboxSessionFork(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("sandbox session fork", flag.ContinueOnError)
	count := fs.Int("count", 1, "sessions to start, within the plan's SandboxForkMax")
	kind := fs.String("kind", "", "the forks' kind: ephemeral (default) or persistent")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || len(positional) != 1 || *count < 1 {
		PrintUsage(os.Stderr, sandboxSessionForkUsage, "sandbox-sessions")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ForkSandbox(context.Background(), positional[0], api.ForkSandboxRequest{Count: *count, Kind: *kind})
	if err != nil {
		return printErr("Could not fork the sandbox session", err)
	}
	for i := range out.Forks {
		out.Forks[i].Token, out.Forks[i].TokenExpiresAt = "", nil
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out.Forks))
	}
	for _, f := range out.Forks {
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s\n", f.ID, f.State, f.Kind)
	}
	return 0
}

func cmdSandboxSessionRm(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale sandbox session delete <id>", "sandbox-sessions")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if _, err := client.CloseSandbox(context.Background(), args[0]); err != nil {
		return printErr("Could not close the sandbox session", err)
	}
	if !jsonOutput {
		PrintOK(osStdout, "Closed sandbox session %s", args[0])
	}
	return 0
}

func sandboxSessionTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func sandboxSessionBytes(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
}
