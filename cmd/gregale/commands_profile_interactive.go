package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"
)

func cmdProfileUseInteractive(args []string, cfg cliConfig) int {
	fs := newFlagSet("profile use", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose and check a saved connection before switching")
	timeout := fs.Duration("timeout", 10*time.Second, "connection check deadline")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if !*interactive || fs.NArg() != 0 || *timeout <= 0 {
		PrintUsage(osStderr, "usage: gregale profile use --interactive [--timeout 10s]", "config")
		return 1
	}
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use profile use <name> and profile check for scripts or JSON"))
	}
	if profileOverride != "" || os.Getenv("FAAS_API") != "" || os.Getenv("FAAS_TOKEN") != "" {
		return printErr("Connection override is active", errors.New("run without --profile, FAAS_API, and FAAS_TOKEN so this flow can check each saved connection and its own credential"))
	}
	names := []string{"default"}
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	labels := make([]string, len(names))
	active := selectedProfile(cfg)
	fallback := 0
	for i, name := range names {
		base, err := savedProfileAPIBase(cfg, name)
		if err != nil {
			return printErr("Invalid saved connection", err)
		}
		labels[i] = name + " · " + base
		if name == active {
			labels[i] += " (active)"
			fallback = i
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	choice, err := prompt.choose(ctx, "Choose a saved connection to check.", labels, fallback)
	if err != nil {
		return startInputExit(err)
	}
	name := names[choice]
	base, _ := savedProfileAPIBase(cfg, name) // All entries were validated above.
	checkCtx, cancel := context.WithTimeout(ctx, *timeout)
	report := checkSavedProfile(checkCtx, name, base, *timeout)
	cancel()
	renderProfileCheck(report)
	if !report.OK {
		return report.ExitCode
	}
	if name == active {
		PrintOK(osStdout, "Profile %s is already active.", name)
		return 0
	}
	confirmed, err := prompt.confirm(ctx, "Use "+name+" as the active connection?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Active connection unchanged.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	// Reload to preserve other configuration edits made during the prompts.
	latest, err := loadCLIConfig()
	if err != nil {
		return printErr("Could not reload profiles", err)
	}
	latestBase, err := savedProfileAPIBase(latest, name)
	if err != nil || latestBase != base || selectedProfile(latest) != active {
		return printErr("Profiles changed during review", errors.New("run the flow again to review the updated connection"))
	}
	latest.ActiveProfile = name
	if err := saveCLIConfig(latest); err != nil {
		return printErr("Could not switch connection", err)
	}
	PrintOK(osStdout, "Active connection: %s (%s)", name, base)
	return 0
}

func savedProfileAPIBase(cfg cliConfig, name string) (string, error) {
	base := cfg.APIBase
	if name != "default" {
		profile, exists := cfg.Profiles[name]
		if !exists {
			return "", fmt.Errorf("profile %s no longer exists", name)
		}
		base = profile.APIBase
	}
	if base == "" {
		base = defaultAPIBase
	}
	validated, err := validateConfigAPIBase(base)
	if err != nil {
		return "", errors.New("saved API address must be an http(s) URL without credentials, query parameters, or fragments")
	}
	return validated, nil
}

func checkSavedProfile(ctx context.Context, name, base string, timeout time.Duration) profileCheckReport {
	previous := profileOverride
	profileOverride = name
	defer func() { profileOverride = previous }()
	token := loadToken()
	source := "none"
	if token != "" {
		source = "profile:" + name
	}
	connection := &connectionContext{Profile: name, ProfileSource: "interactive selection", APIBase: base, APISource: "saved profile"}
	client := NewClient(base, token)
	client.SetCompletionCache(nil)
	client.HTTPClient().Timeout = timeout
	return checkProfileConnection(ctx, client, connection, source, token != "")
}
