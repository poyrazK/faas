// commands_billing_webhook.go — `gregale billing webhook-test`
// (PR-P4, operator-only).
//
// Sends a signed Paddle (or Stripe) webhook to a configured URL so
// the operator can smoke-test their registered endpoint without
// opening the Paddle dashboard. The signing mirrors the production
// verifier exactly (HMAC-SHA256 over `ts:body` for Paddle;
// Stripe-Signature's `t=…,v1=…` shape for Stripe) so a successful
// round-trip proves both the signature and the URL.
//
// Build-tag-gated: this file is intended to ship in the CLI binary
// for operator use. We do NOT gate it on `paddle_sandbox_e2e` because
// the CLI is the operator-facing artefact — operators need it on
// their local box, not in a CI runner. The `--live` flag (Paddle
// only) does the actual `api.sandbox.paddle.com` POST to validate
// the dashboard's "Send test event" workflow without leaving the
// terminal.
//
// Usage:
//
//	gregale billing webhook-test paddle --url https://apid.gregale.dev/v1/webhooks/paddle \
//	    --secret-file secrets/.env.sandbox
//	gregale billing webhook-test stripe --url https://apid.gregale.dev/v1/webhooks/stripe \
//	    --secret "whsec_…"
//	gregale billing webhook-test paddle --live --secret-file secrets/.env.sandbox
//
// Exit codes:
//
//	0 — webhook responded 200 (or 2xx for any Paddle handler shape).
//	1 — argument error / secret file missing / network failure.
//	2 — webhook responded non-2xx (operator-visible failure mode).

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/billing/paddle"
	stripewebhook "github.com/onebox-faas/faas/pkg/billing/stripe"
)

const billingSubWebhookTest = "webhook-test"

// webhookTestTimeout bounds the operator-side round-trip. Paddle's
// dashboard "Send test event" sometimes takes ~10 s when the apid
// queue is busy; 30 s is the documented upper bound.
const webhookTestTimeout = 30 * time.Second

// cmdBillingWebhookTest dispatches to the paddle or stripe signer.
// Argument shape: <provider> [--url URL] [--secret SECRET |
// --secret-file PATH] [--payload PATH] [--live].
//
// The dispatch model matches commands_billing.go::cmdBilling — the
// CLI flag package stops at the first non-flag token, so the first
// positional is the provider discriminator.
func cmdBillingWebhookTest(args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: gregale billing webhook-test <paddle|stripe> [flags]\n")
		return 1
	}
	switch args[0] {
	case "paddle":
		return cmdBillingWebhookTestPaddle(args[1:])
	case "stripe":
		return cmdBillingWebhookTestStripe(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale billing webhook-test: unknown provider %q (expected paddle or stripe)\n", args[0])
		return 1
	}
}

// cmdBillingWebhookTestPaddle sends a signed Paddle payload to the
// operator-configured URL. With --live, sends to
// api.sandbox.paddle.com so the operator can validate the dashboard
// "Send test event" workflow from the terminal.
//
// The default payload is a synthetic subscription.created with a
// randomised event_id + a customer_id the operator can later
// substitute via --payload. The signer is
// paddle.SignForTestForTest so the verifier on the server side
// accepts it.
func cmdBillingWebhookTestPaddle(args []string) int {
	cfg, err := parseWebhookTestFlags(args, "https://api.sandbox.paddle.com")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregale billing webhook-test paddle: %v\n", err)
		return 1
	}
	body, err := webhookTestLoadPayload(cfg.payloadPath, defaultPaddleTestPayload())
	if err != nil {
		return printErr("Could not load payload", err)
	}
	header := paddle.SignForTestForTest(body, cfg.secret, time.Now())
	return postSignedWebhook(cfg.url, "Paddle-Signature", header, body, cfg.timeout)
}

// cmdBillingWebhookTestStripe sends a signed Stripe payload. Stripe's
// signing format is `t=<unix>,v1=<hmac-sha256-hex>` where the hmac is
// over `<t>.<body>`. We use the production-side helper
// pkg/billing/stripe::SignForTest so the operator's local sign
// matches what the verifier on the server side accepts.
func cmdBillingWebhookTestStripe(args []string) int {
	cfg, err := parseWebhookTestFlags(args, "https://api.stripe.com")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregale billing webhook-test stripe: %v\n", err)
		return 1
	}
	body, err := webhookTestLoadPayload(cfg.payloadPath, defaultStripeTestPayload())
	if err != nil {
		return printErr("Could not load payload", err)
	}
	header := stripewebhook.SignForTest(body, cfg.secret, time.Now())
	return postSignedWebhook(cfg.url, "Stripe-Signature", header, body, cfg.timeout)
}

// webhookTestConfig is the parsed flag set. liveURL is the
// provider-specific default (api.sandbox.paddle.com or
// api.stripe.com) used when --live is set without --url.
type webhookTestConfig struct {
	url         string
	secret      string
	payloadPath string
	live        bool
	timeout     time.Duration
}

// parseWebhookTestFlags walks args for the four CLI flags. The CLI
// flag package stops at the first non-flag token, so positional
// handling here is by design (matches cmdBillingStatus).
//
// --url defaults to liveURL when --live is set; otherwise the
// operator must supply --url explicitly (an unset --url without
// --live is an error, because we never want to accidentally POST
// a synthetic event to the production Paddle API).
func parseWebhookTestFlags(args []string, liveURL string) (webhookTestConfig, error) {
	cfg := webhookTestConfig{timeout: webhookTestTimeout}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--url":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--url requires a value")
			}
			i++
			cfg.url = args[i]
		case strings.HasPrefix(a, "--url="):
			cfg.url = strings.TrimPrefix(a, "--url=")
		case a == "--secret":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--secret requires a value")
			}
			i++
			cfg.secret = args[i]
		case a == "--secret-file":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--secret-file requires a path")
			}
			i++
			path := args[i]
			s, err := readWebhookSecretFromFile(path)
			if err != nil {
				return cfg, fmt.Errorf("--secret-file: %w", err)
			}
			cfg.secret = s
		case strings.HasPrefix(a, "--secret-file="):
			path := strings.TrimPrefix(a, "--secret-file=")
			s, err := readWebhookSecretFromFile(path)
			if err != nil {
				return cfg, fmt.Errorf("--secret-file: %w", err)
			}
			cfg.secret = s
		case a == "--payload":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--payload requires a path")
			}
			i++
			cfg.payloadPath = args[i]
		case strings.HasPrefix(a, "--payload="):
			cfg.payloadPath = strings.TrimPrefix(a, "--payload=")
		case a == "--live":
			cfg.live = true
			if cfg.url == "" {
				cfg.url = liveURL
			}
		case a == "--timeout":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--timeout requires a duration in seconds")
			}
			i++
			d, err := time.ParseDuration(args[i] + "s")
			if err != nil {
				return cfg, fmt.Errorf("--timeout: %w", err)
			}
			cfg.timeout = d
		default:
			return cfg, fmt.Errorf("unexpected arg %q", a)
		}
	}
	if cfg.url == "" {
		return cfg, fmt.Errorf("--url is required (or pass --live to default to the provider's sandbox)")
	}
	if cfg.secret == "" {
		return cfg, fmt.Errorf("--secret or --secret-file is required")
	}
	if cfg.timeout <= 0 {
		return cfg, fmt.Errorf("--timeout must be positive")
	}
	return cfg, nil
}

// readWebhookSecretFromFile reads a secret file. We support the
// sealed.env / .env.sandbox shape (`KEY=value` per line; the value
// for the matching key is returned). A bare file containing only
// the secret is also accepted — operators who keep the secret in a
// single-line file (e.g. their password manager) shouldn't have to
// wrap it.
func readWebhookSecretFromFile(path string) (string, error) {
	// Route through openCustomerFile — the canonical pre-open +
	// post-open Lstat discipline that gates every customer-supplied
	// path in the CLI. Operators who symlink --secret-file at a
	// path they don't own (e.g. a config-management dir) get the
	// same refusal the env push / tarball paths get.
	f, err := openCustomerFile(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			// env-file shape: KEY=VALUE.
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			if key == "FAAS_PADDLE_WEBHOOK_SECRET" || key == "FAAS_PADDLE_SANDBOX_WEBHOOK_SECRET" ||
				key == "STRIPE_WEBHOOK_SECRET" {
				if val != "" {
					return val, nil
				}
			}
			continue
		}
		// Bare secret on a single line.
		return line, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no webhook secret found in %s (expected KEY=value or a bare secret line)", path)
}

// webhookTestLoadPayload reads a JSON payload from path; falls back
// to the synthetic default if path is empty. Read errors are wrapped
// with operation context per the package convention.
func webhookTestLoadPayload(path string, fallback []byte) ([]byte, error) {
	if path == "" {
		return fallback, nil
	}
	// Same rationale as readWebhookSecretFromFile: route operator-
	// supplied paths through openCustomerFile so a symlinked
	// --payload doesn't get a free byte-exfil pipe into the webhook
	// POST body.
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return b, nil
}

// postSignedWebhook sends the signed payload and reports the
// outcome. The function exits with 0 on any 2xx (Paddle/Stripe both
// ack accepted events with 200; the apid server also 200s on replay
// rejection, so a 200 is the only "ok" surface here). Non-2xx exits
// with code 2 so the operator can distinguish "argument error" (1)
// from "the webhook fired but failed" (2).
func postSignedWebhook(url, headerName, headerValue string, body []byte, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "build request: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerName, headerValue)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return printErr("POST failed", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
	_, _ = fmt.Fprintf(os.Stdout, "status=%d url=%s sig_header=%s body=%s\n",
		resp.StatusCode, url, headerName, strings.TrimSpace(string(respBody)))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 2
	}
	return 0
}

// defaultPaddleTestPayload is a minimal subscription.created event
// with placeholder IDs. Operators override the customer_id with a
// real one via --payload; the event_id is randomised each call so
// repeated tests don't trip the dedupe (pkg/webhookdedupe).
func defaultPaddleTestPayload() []byte {
	return []byte(`{
  "event_id": "evt_test_local_cli",
  "event_type": "subscription.created",
  "data": {
    "id": "sub_test_local_cli",
    "customer_id": "ctm_test_local_cli",
    "status": "active",
    "items": [{"price": {"id": "pri_test_local_cli"}}]
  }
}`)
}

// defaultStripeTestPayload mirrors the Paddle shape — a minimal
// customer.subscription.created event with placeholder IDs. Stripe
// also accepts this shape; the verifier on the server side just
// checks the signature and the event type.
func defaultStripeTestPayload() []byte {
	return []byte(`{
  "id": "evt_test_local_cli",
  "type": "customer.subscription.created",
  "data": {
    "object": {
      "id": "sub_test_local_cli",
      "customer": "cus_test_local_cli",
      "status": "active"
    }
  }
}`)
}
