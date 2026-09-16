package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	stripewebhook "github.com/onebox-faas/faas/pkg/billing/stripe"
)

const dispatchBilling = "billing"

func cmdBillingDispatch(args []string) int {
	if len(args) == 0 || hasOperatorHelp(args) {
		printOperatorBillingUsage(osStdout)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "price-catalog":
		return cmdOperatorPriceCatalog(args[1:])
	case "reconcile":
		return cmdOperatorBillingReconcile(args[1:])
	case "reconcile-paddle-overage":
		return cmdOperatorPaddlePreflight(args[1:])
	case "webhook-test":
		return cmdOperatorWebhookTest(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl billing: unknown subcommand %q\n", args[0])
		return 2
	}
}

func hasOperatorHelp(args []string) bool {
	for _, arg := range args {
		if arg == flagHelpLong || arg == flagHelpShort {
			return true
		}
	}
	return false
}

func printOperatorBillingUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: gregalectl billing <price-catalog|reconcile|reconcile-paddle-overage|webhook-test> ...")
}

func cmdOperatorPriceCatalog(args []string) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl billing price-catalog <list|sync|reset>")
		return 2
	}
	var (
		method     string
		path       string
		idempotent bool
	)
	switch args[0] {
	case "list":
		method, path = http.MethodGet, "/v1/admin/billing-paddle-catalog"
	case "sync":
		method, path, idempotent = http.MethodPost, "/v1/admin/billing-paddle-catalog/sync", true
	case "reset":
		method, path, idempotent = http.MethodDelete, "/v1/admin/billing-paddle-catalog", true
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl billing price-catalog: unknown action %q\n", args[0])
		return 2
	}
	var response api.BillingCatalogResponse
	if code := operatorBillingRequest(method, path, &response, idempotent); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "provider=%s synced_at=%s entries=%d\n", response.Provider, response.SyncedAt, len(response.Entries))
	for _, entry := range response.Entries {
		_, _ = fmt.Fprintf(osStdout, "%s/%s handle=%s synced_at=%s\n", entry.Plan, entry.Kind, entry.Handle, entry.SyncedAt.UTC().Format(time.RFC3339))
	}
	return 0
}

func cmdOperatorBillingReconcile(args []string) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl billing reconcile <account-id>")
		return 2
	}
	accountID := strings.TrimSpace(args[0])
	if _, err := uuid.Parse(accountID); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing reconcile: account-id must be a UUID")
		return 2
	}
	var response api.BillingReconcileResponse
	path := "/v1/admin/billing-reconcile/" + url.PathEscape(accountID)
	if code := operatorBillingRequest(http.MethodPost, path, &response, true); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "account=%s window=[%s,%s] mb_seconds=%d\n", response.AccountID, response.Start.UTC().Format(time.RFC3339), response.End.UTC().Format(time.RFC3339), response.MBSeconds)
	return 0
}

func cmdOperatorPaddlePreflight(args []string) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl billing reconcile-paddle-overage")
		return 2
	}
	var response api.BillingPaddleOveragePreflightResponse
	if code := operatorBillingRequest(http.MethodGet, "/v1/admin/billing-paddle-overage/preflight", &response, false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "table_exists=%t window_start=%t state=%t claimed_at=%t claimed_by=%t pending=%d completed=%d\n",
		response.TableExists, response.HasWindowStart, response.HasState, response.HasClaimedAt, response.HasClaimedBy, response.PendingRows, response.CompletedRows)
	if !response.TableExists || !response.HasWindowStart || !response.HasState || !response.HasClaimedAt || !response.HasClaimedBy {
		return 3
	}
	return 0
}

func operatorBillingRequest(method, path string, output any, idempotent bool) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing:", err)
		return 1
	}
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), method, path, nil, output, idempotent, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing:", err)
		return 3
	}
	return 0
}

func cmdOperatorWebhookTest(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl billing webhook-test <paddle|stripe> --url URL --secret-file PATH [--payload PATH]")
		return 2
	}
	provider := args[0]
	fs := flag.NewFlagSet("billing webhook-test "+provider, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	targetURL := fs.String("url", "", "webhook URL")
	secretFile := fs.String("secret-file", "", "file containing the signing secret")
	payloadFile := fs.String("payload", "", "JSON payload file")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return 2
	}
	if provider != "paddle" && provider != "stripe" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing webhook-test: provider must be paddle or stripe")
		return 2
	}
	parsed, err := url.ParseRequestURI(*targetURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || strings.TrimSpace(*secretFile) == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing webhook-test: --url and --secret-file are required")
		return 2
	}
	secretBytes, err := os.ReadFile(*secretFile)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing webhook-test: read secret:", err)
		return 2
	}
	secret := strings.TrimSpace(string(secretBytes))
	if secret == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing webhook-test: secret file is empty")
		return 2
	}
	body := []byte(`{"id":"gregalectl-webhook-test","event_type":"subscription.created","data":{}}`)
	if provider == "stripe" {
		body = []byte(`{"id":"evt_gregalectl_test","type":"customer.subscription.created","data":{"object":{}}}`)
	}
	if *payloadFile != "" {
		body, err = os.ReadFile(*payloadFile)
		if err != nil {
			_, _ = fmt.Fprintln(osStderr, "gregalectl billing webhook-test: read payload:", err)
			return 2
		}
	}
	headerName := "Paddle-Signature"
	headerValue := paddle.SignForTestForTest(body, secret, time.Now())
	if provider == "stripe" {
		headerName = "Stripe-Signature"
		headerValue = stripewebhook.SignForTest(body, secret, time.Now())
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, *targetURL, strings.NewReader(string(body)))
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing webhook-test:", err)
		return 2
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerName, headerValue)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl billing webhook-test:", err)
		return 3
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl billing webhook-test: endpoint returned %s\n", resp.Status)
		return 3
	}
	_, _ = fmt.Fprintf(osStdout, "webhook test accepted: %s\n", resp.Status)
	return 0
}
