package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/visibility"
)

type entityInspectionClient interface {
	InspectDurableEntity(context.Context, string, api.DurableEntityInspectRequest) (api.DurableEntityInspectResponse, error)
}
type entityVisibilityCommand struct {
	verb, app         string
	selectors         api.DurableEntityInspectRequest
	timeout, interval time.Duration
}

func parseEntityVisibility(args []string) (entityVisibilityCommand, error) {
	var c entityVisibilityCommand
	if len(args) == 0 || (args[0] != "inspect" && args[0] != "watch") {
		return c, errors.New("use entities inspect or watch")
	}
	c.verb = args[0]
	fs := newFlagSet("entities "+c.verb, flag.ContinueOnError)
	fs.StringVar(&c.app, "app", "", "owned app slug")
	fs.StringVar(&c.selectors.Namespace, "namespace", "", "entity namespace")
	fs.StringVar(&c.selectors.Key, "key", "", "entity key")
	fs.StringVar(&c.selectors.Environment, "environment", "", "exact environment")
	fs.StringVar(&c.selectors.PlatformTenantID, "tenant", "", "owned platform tenant UUID")
	fs.DurationVar(&c.timeout, "timeout", 5*time.Minute, "bounded observation deadline")
	fs.DurationVar(&c.interval, "interval", 5*time.Second, "watch poll interval")
	if err := fs.Parse(args[1:]); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, errors.New("unexpected positional arguments")
	}
	for _, value := range []string{c.app, c.selectors.Namespace, c.selectors.Key} {
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || len(value) > api.MaxDurableEntityIdentityBytes || strings.ContainsFunc(value, unicode.IsControl) {
			return c, errors.New("supply bounded nonblank --app, --namespace and --key selectors")
		}
	}
	if c.timeout <= 0 || c.timeout > time.Hour || c.interval < time.Second || c.interval > time.Minute {
		return c, errors.New("use --timeout greater than zero up to 1h and --interval between 1s and 1m")
	}
	return c, nil
}
func cmdEntities(args []string) int {
	c, err := parseEntityVisibility(args)
	if err != nil {
		PrintUsage(os.Stderr, "usage: gregale entities <inspect|watch> --app SLUG --namespace NAME --key KEY [--environment NAME] [--tenant UUID]", "entities")
		return printErr("Invalid entities command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	err = runEntityVisibility(ctx, client, c, osStdout, jsonOutput, time.Now, waitEntityPoll)
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if err != nil {
		return printErr("Entity inspection failed", err)
	}
	return 0
}
func waitEntityPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func runEntityVisibility(ctx context.Context, client entityInspectionClient, c entityVisibilityCommand, out io.Writer, asJSON bool, now func() time.Time, wait func(context.Context, time.Duration) error) error {
	var previous string
	for {
		in, err := client.InspectDurableEntity(ctx, c.app, c.selectors)
		if err != nil {
			return err
		}
		report := visibility.Summarize(in, now())
		// Ignore observation timestamps but retain retry revisions and delivery changes.
		comparison := report
		comparison.ObservedAt = time.Time{}
		body, err := json.Marshal(comparison)
		if err != nil {
			return err
		}
		if string(body) != previous {
			if err := renderEntityVisibility(out, report, asJSON); err != nil {
				return err
			}
			previous = string(body)
		}
		if c.verb == "inspect" {
			return nil
		}
		pollErr := wait(ctx, c.interval)
		if errors.Is(pollErr, context.DeadlineExceeded) && ctx.Err() != nil {
			return nil //nolint:nilerr // Reaching the requested watch duration completes a successful observation window.
		}
		if pollErr != nil {
			return pollErr
		}
	}
}
func renderEntityVisibility(out io.Writer, r visibility.Report, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(r)
	}
	// JSON-quote user-controlled selectors to avoid terminal control sequences.
	body, err := json.Marshal(r.Inspection.Entity)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "%s entity %s\nversion: %d committed: %t\nalarm: %s attempts: %d\noutbox: %s pending: %d attempts: %d\n", r.ObservedAt.Format(time.RFC3339), body, r.Inspection.Version, r.Inspection.StateCommitted, r.AlarmStatus, r.Inspection.Alarm.Attempts, r.OutboxStatus, r.Inspection.Outbox.Pending, r.Inspection.Outbox.Attempts); err != nil {
		return err
	}
	// Fixed field names plus JSON encoding preserve full observed retry deadlines/IDs safely.
	metadata, err := json.Marshal(struct {
		Alarm    api.DurableEntityAlarmInspection  `json:"alarm"`
		Outbox   api.DurableEntityOutboxInspection `json:"outbox"`
		Revision string                            `json:"recovery_revision"`
	}{r.Inspection.Alarm, r.Inspection.Outbox, r.Inspection.RecoveryRevision})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "recovery: %s\n", metadata); err != nil {
		return err
	}
	for _, finding := range r.Findings {
		if _, err := fmt.Fprintf(out, "%s: %s\n", finding.Code, finding.Detail); err != nil {
			return err
		}
	}
	for _, limitation := range r.Limitations {
		if _, err := fmt.Fprintf(out, "scope: %s\n", limitation); err != nil {
			return err
		}
	}
	return nil
}
