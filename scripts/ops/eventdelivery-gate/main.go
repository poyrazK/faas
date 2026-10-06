// eventdelivery-gate runs the same application recovery gate used by CI
// against two explicitly provisioned acceptance apps on a staging deployment.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/e2etest/eventdelivery"
)

func main() {
	var cfg eventdelivery.Config
	flag.StringVar(&cfg.APIURL, "api-url", "", "staging API base URL")
	flag.StringVar(&cfg.HealthyApp, "healthy-app", "", "successful acceptance app slug")
	flag.StringVar(&cfg.FailingApp, "failing-app", "", "failing acceptance app slug")
	flag.StringVar(&cfg.HealthyURL, "healthy-url", "", "successful acceptance app HTTP base URL")
	flag.StringVar(&cfg.FailingURL, "failing-url", "", "failing acceptance app HTTP base URL")
	flag.StringVar(&cfg.Source, "source", "acceptance.events.delivery", "exact subscription source")
	flag.StringVar(&cfg.EventType, "type", "delivery.recovery", "exact subscription event type")
	flag.StringVar(&cfg.RoutingMode, "routing-mode", "recipient", "expected routing mode: event or recipient")
	flag.IntVar(&cfg.ExpectedAttempts, "attempts", 3, "effective handler retry budget, including the first attempt")
	timeout := flag.Duration("timeout", 2*time.Minute, "total gate deadline")
	checkpoint := flag.Bool("restart-checkpoint", false, "pause at first pending retry; type restarted after restarting schedd")
	flag.Parse()
	cfg.APIToken = os.Getenv("GREGALE_API_TOKEN")
	cfg.ControlToken = os.Getenv("GREGALE_EVENT_GATE_CONTROL_TOKEN")
	cfg.EventID = "delivery-gate-" + uuid.NewString()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	hooks := eventdelivery.Hooks{}
	if *checkpoint {
		hooks.RetryPending = func(ctx context.Context) error { return confirmRestart(ctx, cfg.EventID) }
	}
	report, err := eventdelivery.Run(ctx, cfg, hooks)
	if err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"gate": "fail", "event_id": cfg.EventID, "error": err.Error()})
		os.Exit(1)
	}
	restart := "not_exercised"
	if *checkpoint {
		restart = "operator_confirmed"
	}
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		eventdelivery.Report
		SchedulerRestart string `json:"scheduler_restart"`
	}{Report: report, SchedulerRestart: restart})
}

func confirmRestart(ctx context.Context, eventID string) error {
	_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"phase": "retry_pending", "event_id": eventID,
		"instruction": "Restart the staging scheduler now. Type restarted and press Enter to continue. The gate deadline still applies."})
	answer := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err == nil && strings.TrimSpace(line) != "restarted" {
			err = fmt.Errorf("restart was not confirmed")
		}
		answer <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-answer:
		return err
	}
}
