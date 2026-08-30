// Tests for the alert-preset test-alert handler (issue #1233 /
// ADR-123 PR-C commit 2). Pins the two pure helpers that live
// alongside the orchestrator — buildTestAlertEvent is the
// payload-shaping math, unsealAlertRuleWebhookSecret is the
// secret-recovery path. The orchestrator
// (sendTestAlertPresetCore) is exercised end-to-end via the
// dashboard form handler's test in dashboard_preset_enable_test.go
// (added in the same PR) and via the JSON integration smoke.
//
// Pattern: same as the existing validateAndDeriveEnablePresetOpts
// tests above — table-driven, pure-helper-focused, in package
// main so the test can reach the unexported helpers directly.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestBuildTestAlertEvent_PayloadDiscriminator pins the
// load-bearing shape of the test-alert payload:
//   - payload.test == true on EVERY event (the discriminator the
//     customer's verifier keys off).
//   - delivery_id is 32-char lowercase hex (no UUID dashes) so the
//     customer's audit log join is unambiguous.
//   - observed is on the "breached" side of the threshold for
//     BOTH gt and lt comparisons. A naive threshold × 1.01 for
//     lt would land on the wrong side (above threshold for lt)
//     — that's the bug this test catches.
//   - rule field carries the catalog preset name (matches the
//     production dispatcher shape at pkg/webhook/dispatcher.go:439).
func TestBuildTestAlertEvent_PayloadDiscriminator(t *testing.T) {
	acct := state.Account{ID: "acct-1"}
	app := state.App{ID: "app-1", Slug: "demo"}
	rule := state.AlertRule{ID: "rule-1", Metric: "error_rate_pct"}
	cases := []struct {
		name        string
		preset      state.AlertPreset
		wantObsGt   float64
		wantObsLt   float64
		whichBranch string
	}{
		{
			name: "gt comparison → observed = threshold × 1.01",
			preset: state.AlertPreset{
				Name:        "error_rate_2pct",
				DisplayName: "Error rate exceeds 2%",
				Metric:      "error_rate_pct",
				Comparison:  "gt",
				Threshold:   2.0,
				WindowSpec:  "15m",
			},
			wantObsGt:   2.02,
			whichBranch: "gt",
		},
		{
			name: "lt comparison → observed = threshold × 0.99 (just under)",
			preset: state.AlertPreset{
				Name:        "api_down",
				DisplayName: "API is down",
				Metric:      "api_reachable",
				Comparison:  "lt",
				Threshold:   1.0,
				WindowSpec:  "5m",
			},
			wantObsLt:   0.99,
			whichBranch: "lt",
		},
		{
			name: "unknown comparison → observed == threshold (no synthetic shift)",
			preset: state.AlertPreset{
				Name:        "future_preset",
				DisplayName: "Future preset",
				Metric:      "future_metric",
				Comparison:  "eq",
				Threshold:   42,
				WindowSpec:  "1h",
			},
			whichBranch: "none",
		},
		{
			// /code-review finding: threshold=0 (deploy_failed
			// preset in migrations/00418_alert_presets_seed.sql)
			// must NOT produce observed=0 — the customer's
			// verifier would treat the test as a no-op.
			// 1% of |threshold|=0 falls back to the absolute
			// margin floor of 0.01, so observed = 0 + 0.01.
			name: "gt comparison at threshold=0 → observed = 0 + margin floor",
			preset: state.AlertPreset{
				Name:        "deploy_failed",
				DisplayName: "Recent deployment failed",
				Metric:      "apid_deployment_failed_total",
				Comparison:  "gt",
				Threshold:   0,
				WindowSpec:  "1h",
			},
			wantObsGt:   0.01,
			whichBranch: "gt",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, evt, _, prob := buildTestAlertEvent(acct, app, rule, c.preset)
			if prob != nil {
				t.Fatalf("prob = %v, want nil", prob)
			}
			if !evt.Payload["test"].(bool) {
				t.Errorf("payload.test = %v, want true", evt.Payload["test"])
			}
			// 32-char hex: 16 bytes from crypto/rand encoded as hex.
			if len(id) != 32 || strings.ContainsAny(id, "- ") {
				t.Errorf("delivery_id = %q, want 32-char lowercase hex (no dashes)", id)
			}
			if evt.Rule != c.preset.Name {
				t.Errorf("evt.Rule = %q, want %q", evt.Rule, c.preset.Name)
			}
			if evt.RuleName != c.preset.DisplayName {
				t.Errorf("evt.RuleName = %q, want %q", evt.RuleName, c.preset.DisplayName)
			}
			if evt.AppID != app.Slug {
				t.Errorf("evt.AppID = %q, want %q", evt.AppID, app.Slug)
			}
			gotObs := evt.Payload["observed"].(float64)
			switch c.whichBranch {
			case "gt":
				if gotObs != c.wantObsGt {
					t.Errorf("observed = %v, want %v (gt branch)", gotObs, c.wantObsGt)
				}
			case "lt":
				if gotObs != c.wantObsLt {
					t.Errorf("observed = %v, want %v (lt branch)", gotObs, c.wantObsLt)
				}
			case "none":
				if gotObs != c.preset.Threshold {
					t.Errorf("observed = %v, want %v (no-shift branch)", gotObs, c.preset.Threshold)
				}
			}
		})
	}
}

// TestBuildTestAlertEvent_PayloadMarshalsAsJSON pins the wire
// shape: the dispatcher marshals the Event to JSON; the customer
// sees that JSON. We do a round-trip here so a future struct
// field addition that breaks the test: true discriminator fails
// this test before it fails the dashboard render.
func TestBuildTestAlertEvent_PayloadMarshalsAsJSON(t *testing.T) {
	_, evt, _, prob := buildTestAlertEvent(state.Account{ID: "a"}, state.App{Slug: "demo"}, state.AlertRule{}, state.AlertPreset{
		Name: "api_down", DisplayName: "X", Comparison: "lt", Threshold: 1, WindowSpec: "5m",
	})
	if prob != nil {
		t.Fatalf("buildTestAlertEvent: %v", prob)
	}
	body, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"test":true`) {
		t.Errorf("marshalled body missing test:true discriminator; body=%s", body)
	}
}

// TestUnsealAlertRuleWebhookSecret pins the unseal path:
//   - A sealed blob from a known identity unseals to the
//     original plaintext.
//   - A sealed blob from an UNKNOWN identity (wrong key) fails
//     with an api.Problem (not a panic, not a bare error).
//   - An empty blob returns the same Problem shape (no leak via
//     "successful" empty plaintext).
func TestUnsealAlertRuleWebhookSecret(t *testing.T) {
	const plaintext = "super-secret-webhook-key"
	// Generate a throwaway identity for the test.
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("gen identity: %v", err)
	}
	recipient := ident.Recipient()
	sealed, err := secretbox.SealBytes(recipient, "alert_rule_secret", []byte(plaintext), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Wire the test identity into the unseal path. Restore the
	// original on test exit so other tests aren't poisoned.
	prev := hostIdentitiesForUnseal
	t.Cleanup(func() { hostIdentitiesForUnseal = prev })
	hostIdentitiesForUnseal = func(_ context.Context) []*age.X25519Identity {
		return []*age.X25519Identity{ident}
	}
	t.Run("correct identity unseals", func(t *testing.T) {
		got, prob := unsealAlertRuleWebhookSecret(context.Background(), sealed)
		if prob != nil {
			t.Fatalf("prob = %v, want nil", prob)
		}
		if string(got) != plaintext {
			t.Errorf("plaintext = %q, want %q", got, plaintext)
		}
	})
	t.Run("wrong identity fails cleanly", func(t *testing.T) {
		wrong, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatalf("gen wrong identity: %v", err)
		}
		hostIdentitiesForUnseal = func(_ context.Context) []*age.X25519Identity {
			return []*age.X25519Identity{wrong}
		}
		_, prob := unsealAlertRuleWebhookSecret(context.Background(), sealed)
		if prob == nil {
			t.Fatalf("prob = nil, want ErrCapacity (wrong identity must fail)")
		}
	})
	t.Run("empty identity list fails cleanly", func(t *testing.T) {
		hostIdentitiesForUnseal = func(_ context.Context) []*age.X25519Identity {
			return nil
		}
		_, prob := unsealAlertRuleWebhookSecret(context.Background(), sealed)
		if prob == nil {
			t.Fatalf("prob = nil, want ErrCapacity (no identity loaded)")
		}
	})
	t.Run("empty blob fails cleanly", func(t *testing.T) {
		hostIdentitiesForUnseal = func(_ context.Context) []*age.X25519Identity {
			return []*age.X25519Identity{ident}
		}
		_, prob := unsealAlertRuleWebhookSecret(context.Background(), []byte{})
		if prob == nil {
			t.Fatalf("prob = nil, want ErrCapacity (empty blob)")
		}
	})
}

// TestUnsealAlertRuleWebhookSecret_BadPlaintext guards against a
// future bug where the seal layer silently returns a zero-length
// plaintext (the seal succeeded but the body was empty). The
// handler treats this as a hard error so a misconfigured
// operator (sealed an empty webhook secret at some prior boot)
// doesn't get a working test-alert endpoint that signs nothing.
func TestUnsealAlertRuleWebhookSecret_BadPlaintext(t *testing.T) {
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("gen identity: %v", err)
	}
	recipient := ident.Recipient()
	sealed, err := secretbox.SealBytes(recipient, "alert_rule_secret", []byte(""), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	prev := hostIdentitiesForUnseal
	t.Cleanup(func() { hostIdentitiesForUnseal = prev })
	hostIdentitiesForUnseal = func(_ context.Context) []*age.X25519Identity {
		return []*age.X25519Identity{ident}
	}
	_, prob := unsealAlertRuleWebhookSecret(context.Background(), sealed)
	if prob == nil {
		t.Fatalf("prob = nil, want ErrCapacity (empty plaintext must refuse)")
	}
}
