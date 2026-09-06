package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/githubd"
)

const dispatchGithub = "github"

type githubRecoveryStore interface {
	ListWebhookDeliveries(context.Context, string, int) ([]githubd.WebhookDeliveryRecord, error)
	RetryWebhookDelivery(context.Context, string) (bool, error)
	ListCheckUpdates(context.Context, string, int) ([]githubd.CheckUpdateRecord, error)
	RetryCheckUpdate(context.Context, string) (bool, error)
}

type pgGithubRecoveryStore struct {
	deliveries *githubd.PGWebhookStore
	checks     *githubd.PGCheckUpdateStore
}

func (s pgGithubRecoveryStore) ListWebhookDeliveries(ctx context.Context, status string, limit int) ([]githubd.WebhookDeliveryRecord, error) {
	return s.deliveries.ListWebhookDeliveries(ctx, status, limit)
}

func (s pgGithubRecoveryStore) RetryWebhookDelivery(ctx context.Context, id string) (bool, error) {
	return s.deliveries.RetryWebhookDelivery(ctx, id)
}

func (s pgGithubRecoveryStore) ListCheckUpdates(ctx context.Context, status string, limit int) ([]githubd.CheckUpdateRecord, error) {
	return s.checks.ListCheckUpdates(ctx, status, limit)
}

func (s pgGithubRecoveryStore) RetryCheckUpdate(ctx context.Context, id string) (bool, error) {
	return s.checks.RetryCheckUpdate(ctx, id)
}

var githubRecoveryOpener = func() (githubRecoveryStore, func(), error) {
	pool, err := openPgPoolFromEnv(context.Background())
	if err != nil {
		return nil, func() {}, fmt.Errorf("gregalectl github: %w", err)
	}
	return pgGithubRecoveryStore{
		deliveries: githubd.NewPGWebhookDeliveryStore(pool),
		checks:     githubd.NewPGCheckUpdateStore(pool),
	}, pool.Close, nil
}

func cmdGithubDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl github: missing subcommand; want status|retry-delivery|retry-check")
		return 2
	}
	switch args[0] {
	case "status":
		return cmdGithubStatus(args[1:])
	case "retry-delivery":
		return cmdGithubRetryDelivery(args[1:])
	case "retry-check":
		return cmdGithubRetryCheck(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl github: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdGithubStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	status := fs.String("status", "dead", "queue status: pending|processing|succeeded|dead (empty lists all)")
	limit := fs.Int("limit", 100, "maximum rows per queue (1..500)")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > 500 {
		fmt.Fprintln(os.Stderr, "gregalectl github status: no positional args; --limit must be 1..500")
		return 2
	}
	switch *status {
	case "", "pending", "processing", "succeeded", "dead":
	default:
		fmt.Fprintln(os.Stderr, "gregalectl github status: --status must be pending|processing|succeeded|dead (or empty)")
		return 2
	}
	store, closeFn, err := githubRecoveryOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()
	ctx := context.Background()
	deliveries, err := store.ListWebhookDeliveries(ctx, *status, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl github status:", err)
		return 1
	}
	checks, err := store.ListCheckUpdates(ctx, *status, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl github status:", err)
		return 1
	}
	if *jsonOut || jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Deliveries []githubd.WebhookDeliveryRecord `json:"deliveries"`
			Checks     []githubd.CheckUpdateRecord     `json:"check_updates"`
		}{deliveries, checks}); err != nil {
			fmt.Fprintln(os.Stderr, "gregalectl github status:", err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "deliveries=%d check_updates=%d status=%s\n", len(deliveries), len(checks), *status)
	for _, d := range deliveries {
		_, _ = fmt.Fprintf(os.Stdout, "delivery %s event=%s status=%s attempts=%d updated=%s error=%q\n",
			d.DeliveryID, d.EventType, d.Status, d.Attempts, d.UpdatedAt.UTC().Format(time.RFC3339), d.LastError)
	}
	for _, c := range checks {
		_, _ = fmt.Fprintf(os.Stdout, "check deployment=%s generation=%d status=%s attempts=%d updated=%s error=%q\n",
			c.DeploymentID, c.Generation, c.Status, c.Attempts, c.UpdatedAt.UTC().Format(time.RFC3339), c.LastError)
	}
	return 0
}

func cmdGithubRetryDelivery(args []string) int {
	fs := flag.NewFlagSet("retry-delivery", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	deliveryID := fs.String("delivery-id", "", "dead X-GitHub-Delivery id")
	yes := fs.Bool("yes", false, "acknowledge retrying customer deployment work")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *deliveryID == "" || !*yes || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl github retry-delivery: --delivery-id and --yes required")
		return 2
	}
	store, closeFn, err := githubRecoveryOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()
	retried, err := store.RetryWebhookDelivery(context.Background(), *deliveryID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl github retry-delivery:", err)
		return 1
	}
	if !retried {
		fmt.Fprintln(os.Stderr, "gregalectl github retry-delivery: delivery not found or not dead")
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "retried delivery=%s\n", *deliveryID)
	return 0
}

func cmdGithubRetryCheck(args []string) int {
	fs := flag.NewFlagSet("retry-check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	deploymentID := fs.String("deployment-id", "", "deployment id whose dead Check Run update should retry")
	yes := fs.Bool("yes", false, "acknowledge retrying the GitHub Check Run write")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *deploymentID == "" || !*yes || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl github retry-check: --deployment-id and --yes required")
		return 2
	}
	store, closeFn, err := githubRecoveryOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()
	retried, err := store.RetryCheckUpdate(context.Background(), *deploymentID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl github retry-check:", err)
		return 1
	}
	if !retried {
		fmt.Fprintln(os.Stderr, "gregalectl github retry-check: update not found or not dead")
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "retried check deployment=%s\n", *deploymentID)
	return 0
}
