package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

type obsDeploymentOptions struct {
	release, report string
	after           time.Time
	wait, poll      time.Duration
	json            bool
}

func cmdObsVerifyDeployment(args []string) int {
	options, cred, ok := parseObsDeploymentOptions(args)
	if !ok {
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client := &http.Client{Timeout: api.ObsPostDeployHTTPTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	fetch := func(ctx context.Context) obsDeploymentSnapshot { return fetchObsDeploymentSnapshot(ctx, client, cred) }
	save := func(report obsDeploymentReport) error {
		if options.report == "" {
			return nil
		}
		return writeObsDeploymentReport(options.report, report)
	}
	report, code := runObsDeploymentVerification(ctx, options, fetch, time.Now, waitObsDeploymentPoll, save)
	if options.json || jsonOutput || jsonFlagEnabled(args) {
		if emitOperatorJSON(report) != 0 {
			return 2
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "monitoring=%s reason=%s release=%s attempts=%d wait_seconds=%g\n", report.State, report.Reason, report.Release, report.Attempts, report.WaitSeconds)
		for _, check := range report.Checks {
			_, _ = fmt.Fprintf(osStdout, "  check=%s state=%s reason=%s runbook=%s\n", check.Name, check.State, check.Reason, check.Runbook)
		}
	}
	return code
}

func parseObsDeploymentOptions(args []string) (obsDeploymentOptions, obsCredential, bool) {
	fs := flag.NewFlagSet(subObsVerifyDeployment, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	opts := obsDeploymentOptions{}
	fs.StringVar(&opts.release, "release", "", "requested release SHA")
	after := fs.String("after", "", "rollout evidence boundary")
	fs.DurationVar(&opts.wait, "wait-timeout", 0, "wait budget; zero derives from probe policy")
	fs.DurationVar(&opts.poll, "poll-interval", api.ObsPostDeployPollDefault, "poll interval")
	fs.StringVar(&opts.report, "report", "", "private JSON report path")
	fs.BoolVar(&opts.json, "json", false, "emit JSON")
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	stdin := fs.Bool("token-stdin", false, "read bearer token from stdin")
	if err := fs.Parse(args); err != nil {
		return opts, obsCredential{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, *after)
	if err != nil || fs.NArg() != 0 || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(opts.release) || at.After(time.Now().Add(api.ObsHealthClockSkewTolerance)) || opts.wait < 0 || opts.wait > api.ObsPostDeployWaitMax || opts.poll < time.Second || opts.poll > api.ObsPostDeployPollMax || *stdin && *token != "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs verify-deployment: valid release, boundary, bounded wait and poll interval required")
		return opts, obsCredential{}, false
	}
	opts.after = at.UTC()
	if *stdin {
		reader := bufio.NewReader(io.LimitReader(os.Stdin, int64(api.ObsPostDeployTokenMaxBytes)+2))
		value, err := reader.ReadString('\n')
		value = strings.TrimSuffix(value, "\n")
		if err != nil || strings.TrimSpace(value) == "" || len(value) > api.ObsPostDeployTokenMaxBytes || strings.ContainsAny(value, "\r\n\x00") {
			_, _ = fmt.Fprintln(osStderr, "gregalectl obs verify-deployment: one nonempty bearer token line is required")
			return opts, obsCredential{}, false
		}
		return opts, obsCredential{bearer: value}, true
	}
	cred, ok := resolveObsCredential("obs verify-deployment", *token)
	return opts, cred, ok
}

func writeObsDeploymentReport(path string, report obsDeploymentReport) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".monitoring-report-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = file.Write(append(body, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func waitObsDeploymentPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func fetchObsDeploymentSnapshot(ctx context.Context, client *http.Client, cred obsCredential) obsDeploymentSnapshot {
	serving := make(chan obsDeploymentServingRead, 1)
	go func() {
		var out api.ObsServingHealth
		err := fetchObsBundleSection(ctx, client, apidBase()+operatorobs.ServingHealthPath, cred, subObsServingHealth, &out)
		serving <- obsDeploymentServingRead{out, err}
	}()
	var notifications api.ObsNotificationHealth
	err := fetchObsBundleSection(ctx, client, apidBase()+operatorobs.NotificationHealthPath, cred, subObsNotificationHealth, &notifications)
	return obsDeploymentSnapshot{serving: <-serving, notifications: notifications, notificationErr: err}
}
